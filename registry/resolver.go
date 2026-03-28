package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/solidarity-ai/toolbox/packaging"
)

// Resolver orchestrates cache-check → source-fetch → cache-write → load
// for registry packages. Sources are tried in order; a source returning
// ErrReleaseNotFound causes the next source to be tried, while any other
// error short-circuits the chain immediately.
type Resolver struct {
	cache   *Cache
	sources []PackageSource
}

// NewResolver returns a Resolver that checks the given cache first, then
// tries the provided sources in order.
func NewResolver(cache *Cache, sources ...PackageSource) *Resolver {
	return &Resolver{cache: cache, sources: sources}
}

// Resolve returns a loaded package for the given module and version.
// It checks the cache first; on a miss it walks the source chain,
// writes through the cache on success, and loads via Cache.LoadArchive
// so the same integrity path as AddFromArchive is exercised.
func (r *Resolver) Resolve(ctx context.Context, module ModulePath, version Version) (packaging.LoadedPackage, error) {
	if r.cache.Has(module, version) {
		pkg, err := r.cache.LoadArchive(module, version)
		if err != nil {
			return packaging.LoadedPackage{}, fmt.Errorf("resolve %s@%s: cache hit but load failed: %w", module, version, err)
		}
		return pkg, nil
	}

	archive, manifest, err := r.fetchFromSources(ctx, module, version)
	if err != nil {
		return packaging.LoadedPackage{}, fmt.Errorf("resolve %s@%s: %w", module, version, err)
	}

	if err := r.cache.Put(module, version, archive, manifest); err != nil {
		return packaging.LoadedPackage{}, fmt.Errorf("resolve %s@%s: cache write failed: %w", module, version, err)
	}

	pkg, err := r.cache.LoadArchive(module, version)
	if err != nil {
		return packaging.LoadedPackage{}, fmt.Errorf("resolve %s@%s: cache load after write failed: %w", module, version, err)
	}
	return pkg, nil
}

func (r *Resolver) fetchFromSources(ctx context.Context, module ModulePath, version Version) ([]byte, []byte, error) {
	if len(r.sources) == 0 {
		return nil, nil, fmt.Errorf("no sources configured")
	}

	for i, src := range r.sources {
		archive, manifest, err := src.Fetch(ctx, module, version)
		if err == nil {
			return archive, manifest, nil
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			return nil, nil, fmt.Errorf("source[%d]: %w", i, err)
		}
		// ErrReleaseNotFound — try next source
	}

	return nil, nil, fmt.Errorf("all %d sources exhausted: %w", len(r.sources), ErrReleaseNotFound)
}
