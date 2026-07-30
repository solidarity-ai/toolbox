package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/safeguard"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// ResolveResult carries the loaded package plus provenance metadata that can be
// persisted in a declarative toolset lockfile.
type ResolveResult struct {
	Package  packaging.LoadedPackage
	Metadata ResolveMetadata
}

// Resolver orchestrates cache-check → source-fetch → cache-write → load
// for registry packages. Sources are tried in order; a source returning
// ErrReleaseNotFound or ErrSourceUnavailable causes the next source to be
// tried, while any other error short-circuits the chain immediately.
type Resolver struct {
	cache          *Cache
	sources        []PackageSource
	guard          safeguard.PackageGuard
	toolboxVersion Version
}

// NewResolver returns a Resolver that checks the given cache first, then
// tries the provided sources in order.
func NewResolver(cache *Cache, sources ...PackageSource) *Resolver {
	return &Resolver{cache: cache, sources: sources}
}

// SetPackageGuard installs a revocation check that runs before package use,
// again before a cached or fetched package is trusted, and while filtering
// version listings.
func (r *Resolver) SetPackageGuard(guard safeguard.PackageGuard) {
	r.guard = guard
}

func (r *Resolver) PackageGuard() safeguard.PackageGuard {
	return r.guard
}

// SetToolboxVersion enables package compatibility checks while loading archives.
func (r *Resolver) SetToolboxVersion(version Version) {
	r.toolboxVersion = version
}

// CheckPackageCompatibility rejects packages that require a newer Toolbox.
func (r *Resolver) CheckPackageCompatibility(pkg tooldef.Package) error {
	if r.toolboxVersion == "" || pkg.MinimumToolboxVersion == "" {
		return nil
	}
	if tooldef.CompareVersions(r.toolboxVersion, pkg.MinimumToolboxVersion) < 0 {
		return fmt.Errorf("package %s requires Toolbox %s or newer (running %s)", pkg.Module, pkg.MinimumToolboxVersion, r.toolboxVersion)
	}
	return nil
}

// Resolve returns a loaded package plus any provenance metadata available from
// cache verification or source fetches.
func (r *Resolver) Resolve(ctx context.Context, module ModulePath, version Version) (ResolveResult, error) {
	return r.ResolveWithExpected(ctx, module, version, nil)
}

// ResolveWithExpected verifies cached or fetched bytes against expected lock
// metadata when provided. Cache mismatches are treated as untrusted: the cache
// is ignored, one refetch is attempted, and a second mismatch fails.
func (r *Resolver) ResolveWithExpected(ctx context.Context, module ModulePath, version Version, expected *ResolveMetadata) (ResolveResult, error) {
	if expected != nil {
		if err := expected.Validate(); err != nil {
			return ResolveResult{}, fmt.Errorf("resolve %s@%s: invalid expected metadata: %w", module, version, err)
		}
	}
	if err := r.checkPackage(ctx, module, version); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: %w", module, version, err)
	}

	if r.cache.Has(module, version) {
		archiveSHA, err := r.cache.ArchiveSHA256(module, version)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("resolve %s@%s: cache hit but archive hash failed: %w", module, version, err)
		}
		if expected == nil || strings.EqualFold(archiveSHA, expected.ArchiveSHA256) {
			if err := r.checkPackage(ctx, module, version); err != nil {
				return ResolveResult{}, fmt.Errorf("resolve %s@%s: %w", module, version, err)
			}
			pkg, err := r.cache.LoadArchive(module, version, r.CheckPackageCompatibility)
			if err != nil {
				return ResolveResult{}, fmt.Errorf("resolve %s@%s: cache hit but load failed: %w", module, version, err)
			}
			metadata := ResolveMetadata{ArchiveSHA256: archiveSHA}
			if expected != nil {
				metadata = *expected
				metadata.ArchiveSHA256 = archiveSHA
			}
			return ResolveResult{Package: pkg, Metadata: metadata}, nil
		}
	}

	fetch, err := r.fetchFromSources(ctx, module, version)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: %w", module, version, err)
	}
	if err := fetch.Metadata.Validate(); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: source returned invalid metadata: %w", module, version, err)
	}
	if expected != nil && !strings.EqualFold(fetch.Metadata.ArchiveSHA256, expected.ArchiveSHA256) {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: cache mismatch refetch failed integrity check for provenance %s: expected archive_sha256 %s, got %s", module, version, fetch.Metadata.ResolvedFrom, expected.ArchiveSHA256, fetch.Metadata.ArchiveSHA256)
	}
	if err := r.checkPackage(ctx, module, version); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: %w", module, version, err)
	}

	if err := r.cache.Put(module, version, fetch.Archive, fetch.Manifest); err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: cache write failed: %w", module, version, err)
	}

	pkg, err := r.cache.LoadArchive(module, version, r.CheckPackageCompatibility)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("resolve %s@%s: cache load after write failed: %w", module, version, err)
	}
	return ResolveResult{Package: pkg, Metadata: fetch.Metadata}, nil
}

func (r *Resolver) fetchFromSources(ctx context.Context, module ModulePath, version Version) (FetchResult, error) {
	if len(r.sources) == 0 {
		return FetchResult{}, fmt.Errorf("no sources configured")
	}

	for i, src := range r.sources {
		fetch, err := src.Fetch(ctx, module, version)
		if err == nil {
			return fetch, nil
		}
		if !errors.Is(err, ErrReleaseNotFound) && !errors.Is(err, ErrSourceUnavailable) {
			return FetchResult{}, fmt.Errorf("source[%d]: %w", i, err)
		}
		// ErrReleaseNotFound or ErrSourceUnavailable — try next source
	}

	return FetchResult{}, fmt.Errorf("all %d sources exhausted: %w", len(r.sources), ErrReleaseNotFound)
}

// ListVersions returns the deduplicated available versions for a module across
// any configured sources that support version discovery, sorted newest-first.
func (r *Resolver) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	if len(r.sources) == 0 {
		return nil, fmt.Errorf("list versions for %s: no sources configured", module)
	}

	versions := make([]Version, 0)
	seen := make(map[Version]struct{})
	versionSources := 0
	for i, src := range r.sources {
		lister, ok := src.(VersionSource)
		if !ok {
			continue
		}
		versionSources++
		listed, err := lister.ListVersions(ctx, module)
		if err != nil {
			if errors.Is(err, ErrReleaseNotFound) || errors.Is(err, ErrSourceUnavailable) {
				continue
			}
			return nil, fmt.Errorf("list versions for %s: source[%d]: %w", module, i, err)
		}
		for _, version := range listed {
			if _, ok := seen[version]; ok {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, version)
		}
	}
	if versionSources == 0 {
		return nil, fmt.Errorf("list versions for %s: no version-capable sources configured", module)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("list versions for %s: %w", module, ErrReleaseNotFound)
	}
	tooldef.SortVersionsDesc(versions)
	allowed := versions[:0]
	var firstBlocked error
	for _, version := range versions {
		err := r.checkPackage(ctx, module, version)
		if err == nil {
			allowed = append(allowed, version)
			continue
		}
		var blocked *safeguard.BlockedError
		if errors.As(err, &blocked) {
			if firstBlocked == nil {
				firstBlocked = err
			}
			continue
		}
		return nil, fmt.Errorf("list versions for %s: security policy: %w", module, err)
	}
	if len(allowed) == 0 && firstBlocked != nil {
		return nil, fmt.Errorf("list versions for %s: %w", module, firstBlocked)
	}
	return allowed, nil
}

func (r *Resolver) checkPackage(ctx context.Context, module ModulePath, version Version) error {
	if r.guard == nil {
		return nil
	}
	return r.guard.CheckPackage(ctx, module, version)
}
