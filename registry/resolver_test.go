package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/registry/testutil/gitfixture"
)

// mockSource is a test PackageSource that returns preconfigured results.
type mockSource struct {
	result     FetchResult
	err        error
	called     int
	versions   []Version
	versionErr error
	listCalled int
}

func (m *mockSource) Fetch(_ context.Context, _ ModulePath, _ Version) (FetchResult, error) {
	m.called++
	return m.result, m.err
}

func (m *mockSource) ListVersions(_ context.Context, _ ModulePath) ([]Version, error) {
	m.listCalled++
	return m.versions, m.versionErr
}

type countingSource struct {
	inner  PackageSource
	called int
}

func (s *countingSource) Fetch(ctx context.Context, module ModulePath, version Version) (FetchResult, error) {
	s.called++
	return s.inner.Fetch(ctx, module, version)
}

func TestResolver(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	module := ModulePath("example.com/acme/calc")
	version := Version("v1.2.3")
	ctx := context.Background()
	goodMetadata := ResolveMetadata{
		ArchiveSHA256: sha256Hex(archiveBytes),
		GitSHA:        strings.Repeat("a", 40),
		ResolvedFrom:  ResolvedFromGitHubRelease,
		ResolvedAt:    "2026-03-28T12:00:00Z",
	}

	t.Run("CacheHitBypassesSources", func(t *testing.T) {
		cache := newTempCache(t)
		if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		src := &mockSource{err: fmt.Errorf("should not be called")}
		resolver := NewResolver(cache, src)

		result, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if result.Package.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", result.Package.Package.Name, "calc")
		}
		if result.Metadata.ArchiveSHA256 != sha256Hex(archiveBytes) {
			t.Fatalf("archive sha = %q, want %q", result.Metadata.ArchiveSHA256, sha256Hex(archiveBytes))
		}
		if src.called != 0 {
			t.Fatal("source was called despite cache hit")
		}
	})

	t.Run("CacheHitWithExpectedMetadataReturnsLockMetadata", func(t *testing.T) {
		cache := newTempCache(t)
		if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		resolver := NewResolver(cache)
		result, err := resolver.ResolveWithExpected(ctx, module, version, &goodMetadata)
		if err != nil {
			t.Fatalf("ResolveWithExpected() error: %v", err)
		}
		if result.Metadata != goodMetadata {
			t.Fatalf("metadata = %#v, want %#v", result.Metadata, goodMetadata)
		}
	})

	t.Run("PrimarySourceSuccessPopulatesCache", func(t *testing.T) {
		cache := newTempCache(t)
		src := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: goodMetadata}}
		resolver := NewResolver(cache, src)

		result, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if result.Package.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", result.Package.Package.Name, "calc")
		}
		if src.called != 1 {
			t.Fatalf("source called %d times, want 1", src.called)
		}
		if !cache.Has(module, version) {
			t.Fatal("cache.Has() = false after successful resolve, want true")
		}
	})

	t.Run("PseudoVersionFetchPopulatesCacheAndSecondResolveHitsCache", func(t *testing.T) {
		meta := gitfixture.CreatePseudoVersionRepoFromDir(t, fixtureSourceDir(t, "calc"))
		cache := newTempCache(t)
		src := &countingSource{inner: &GitSourceFallback{URLPrefix: "file://"}}
		resolver := NewResolver(cache, src)
		module := ModulePath(meta.RepoDir)
		version := mustVersion(t, meta.PseudoVersion)

		first, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("first Resolve() error: %v", err)
		}
		if first.Package.Package.Name != "calc" {
			t.Fatalf("first package name = %q, want %q", first.Package.Package.Name, "calc")
		}
		if src.called != 1 {
			t.Fatalf("source called %d times after first resolve, want 1", src.called)
		}
		if !cache.Has(module, version) {
			t.Fatal("cache.Has() = false after pseudo-version resolve, want true")
		}
		archiveSHA, err := cache.ArchiveSHA256(module, version)
		if err != nil {
			t.Fatalf("ArchiveSHA256() error: %v", err)
		}
		if archiveSHA != first.Metadata.ArchiveSHA256 {
			t.Fatalf("cache archive sha = %q, want %q", archiveSHA, first.Metadata.ArchiveSHA256)
		}
		if first.Metadata.GitSHA != strings.ToLower(meta.CommitSHA) {
			t.Fatalf("git_sha = %q, want %q", first.Metadata.GitSHA, strings.ToLower(meta.CommitSHA))
		}
		if first.Metadata.ResolvedFrom != ResolvedFromGitSource {
			t.Fatalf("resolved_from = %q, want %q", first.Metadata.ResolvedFrom, ResolvedFromGitSource)
		}

		second, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("second Resolve() error: %v", err)
		}
		if second.Package.Package.Name != "calc" {
			t.Fatalf("second package name = %q, want %q", second.Package.Package.Name, "calc")
		}
		if src.called != 1 {
			t.Fatalf("source called %d times after second resolve, want 1", src.called)
		}
		if second.Metadata.ArchiveSHA256 != first.Metadata.ArchiveSHA256 {
			t.Fatalf("second archive sha = %q, want %q", second.Metadata.ArchiveSHA256, first.Metadata.ArchiveSHA256)
		}
	})

	t.Run("FallbackOnErrReleaseNotFound", func(t *testing.T) {
		cache := newTempCache(t)
		primary := &mockSource{err: fmt.Errorf("primary: %w", ErrReleaseNotFound)}
		fallbackMetadata := goodMetadata
		fallbackMetadata.ResolvedFrom = ResolvedFromGitSource
		fallback := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: fallbackMetadata}}
		resolver := NewResolver(cache, primary, fallback)

		result, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if result.Package.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", result.Package.Package.Name, "calc")
		}
		if result.Metadata.ResolvedFrom != ResolvedFromGitSource {
			t.Fatalf("resolved_from = %q, want %q", result.Metadata.ResolvedFrom, ResolvedFromGitSource)
		}
		if primary.called != 1 {
			t.Fatal("primary source was not called")
		}
		if fallback.called != 1 {
			t.Fatal("fallback source was not called")
		}
	})

	t.Run("FallbackOnErrSourceUnavailable", func(t *testing.T) {
		cache := newTempCache(t)
		primary := &mockSource{err: fmt.Errorf("primary: %w", ErrSourceUnavailable)}
		fallbackMetadata := goodMetadata
		fallbackMetadata.ResolvedFrom = ResolvedFromGitSource
		fallback := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: fallbackMetadata}}
		resolver := NewResolver(cache, primary, fallback)

		result, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if result.Metadata.ResolvedFrom != ResolvedFromGitSource {
			t.Fatalf("resolved_from = %q, want %q", result.Metadata.ResolvedFrom, ResolvedFromGitSource)
		}
		if primary.called != 1 {
			t.Fatal("primary source was not called")
		}
		if fallback.called != 1 {
			t.Fatal("fallback source was not called")
		}
	})

	t.Run("NoFallbackOnNon404Error", func(t *testing.T) {
		cache := newTempCache(t)
		primary := &mockSource{err: fmt.Errorf("network timeout")}
		fallback := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: goodMetadata}}
		resolver := NewResolver(cache, primary, fallback)

		_, err := resolver.Resolve(ctx, module, version)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if primary.called != 1 {
			t.Fatal("primary source was not called")
		}
		if fallback.called != 0 {
			t.Fatal("fallback source was called despite non-404 primary error")
		}
	})

	t.Run("CacheMismatchRefetchesOnceAndSucceeds", func(t *testing.T) {
		cache := newTempCache(t)
		if err := cache.Put(module, version, []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		src := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: goodMetadata}}
		resolver := NewResolver(cache, src)

		result, err := resolver.ResolveWithExpected(ctx, module, version, &goodMetadata)
		if err != nil {
			t.Fatalf("ResolveWithExpected() error: %v", err)
		}
		if result.Package.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", result.Package.Package.Name, "calc")
		}
		if src.called != 1 {
			t.Fatalf("source called %d times, want 1", src.called)
		}
		archiveSHA, err := cache.ArchiveSHA256(module, version)
		if err != nil {
			t.Fatalf("ArchiveSHA256() error: %v", err)
		}
		if archiveSHA != goodMetadata.ArchiveSHA256 {
			t.Fatalf("cache archive sha = %q, want %q", archiveSHA, goodMetadata.ArchiveSHA256)
		}
	})

	t.Run("CacheMismatchRefetchFailureSurfacesExpectedVsActual", func(t *testing.T) {
		cache := newTempCache(t)
		if err := cache.Put(module, version, []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		badFetch := goodMetadata
		badFetch.ArchiveSHA256 = strings.Repeat("b", 64)
		src := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: badFetch}}
		resolver := NewResolver(cache, src)

		_, err := resolver.ResolveWithExpected(ctx, module, version, &goodMetadata)
		if err == nil {
			t.Fatal("ResolveWithExpected() error = nil, want non-nil")
		}
		if src.called != 1 {
			t.Fatalf("source called %d times, want 1", src.called)
		}
		assertErrorContains(t, err, "cache mismatch refetch failed integrity check")
		assertErrorContains(t, err, "expected archive_sha256")
		assertErrorContains(t, err, badFetch.ArchiveSHA256)
		assertErrorContains(t, err, goodMetadata.ArchiveSHA256)
	})

	t.Run("InvalidExpectedMetadataFailsBeforeFetch", func(t *testing.T) {
		cache := newTempCache(t)
		src := &mockSource{result: FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: goodMetadata}}
		resolver := NewResolver(cache, src)
		invalid := goodMetadata
		invalid.GitSHA = "not-a-sha"

		_, err := resolver.ResolveWithExpected(ctx, module, version, &invalid)
		if err == nil {
			t.Fatal("ResolveWithExpected() error = nil, want non-nil")
		}
		if src.called != 0 {
			t.Fatalf("source called %d times, want 0", src.called)
		}
		assertErrorContains(t, err, "invalid expected metadata")
	})

	t.Run("AllSourcesExhausted", func(t *testing.T) {
		cache := newTempCache(t)
		s1 := &mockSource{err: fmt.Errorf("s1: %w", ErrReleaseNotFound)}
		s2 := &mockSource{err: fmt.Errorf("s2: %w", ErrReleaseNotFound)}
		resolver := NewResolver(cache, s1, s2)

		_, err := resolver.Resolve(ctx, module, version)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("error should wrap ErrReleaseNotFound, got: %v", err)
		}
		if s1.called != 1 || s2.called != 1 {
			t.Fatal("not all sources were tried")
		}
	})

	t.Run("NoSourcesConfigured", func(t *testing.T) {
		cache := newTempCache(t)
		resolver := NewResolver(cache)

		_, err := resolver.Resolve(ctx, module, version)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
	})
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want substring %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}

func TestResolverListVersions(t *testing.T) {
	t.Parallel()

	module := ModulePath("example.com/acme/calc")
	ctx := context.Background()

	t.Run("SkipsReleaseNotFoundAndSourceUnavailable", func(t *testing.T) {
		t.Parallel()

		cache := newTempCache(t)
		s1 := &mockSource{versionErr: fmt.Errorf("registry timeout: %w", ErrSourceUnavailable)}
		s2 := &mockSource{versionErr: fmt.Errorf("github miss: %w", ErrReleaseNotFound)}
		s3 := &mockSource{versions: []Version{mustVersion(t, "v1.2.0"), mustVersion(t, "v1.0.0")}}
		resolver := NewResolver(cache, s1, s2, s3)

		versions, err := resolver.ListVersions(ctx, module)
		if err != nil {
			t.Fatalf("ListVersions() error: %v", err)
		}
		if got, want := []string{versions[0].String(), versions[1].String()}, []string{"v1.2.0", "v1.0.0"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("versions = %#v, want %#v", got, want)
		}
		if s1.listCalled != 1 || s2.listCalled != 1 || s3.listCalled != 1 {
			t.Fatalf("listCalled = (%d, %d, %d), want (1, 1, 1)", s1.listCalled, s2.listCalled, s3.listCalled)
		}
	})

	t.Run("HardFailsOnUnexpectedError", func(t *testing.T) {
		t.Parallel()

		cache := newTempCache(t)
		primary := &mockSource{versionErr: fmt.Errorf("unexpected status 403")}
		fallback := &mockSource{versions: []Version{mustVersion(t, "v1.0.0")}}
		resolver := NewResolver(cache, primary, fallback)

		_, err := resolver.ListVersions(ctx, module)
		if err == nil {
			t.Fatal("ListVersions() error = nil, want non-nil")
		}
		if primary.listCalled != 1 {
			t.Fatal("primary source was not called")
		}
		if fallback.listCalled != 0 {
			t.Fatal("fallback source was called despite hard failure")
		}
		assertErrorContains(t, err, "unexpected status 403")
	})

	t.Run("ReturnsNotFoundWhenOnlySkippableErrorsOccur", func(t *testing.T) {
		t.Parallel()

		cache := newTempCache(t)
		s1 := &mockSource{versionErr: fmt.Errorf("registry timeout: %w", ErrSourceUnavailable)}
		s2 := &mockSource{versionErr: fmt.Errorf("github miss: %w", ErrReleaseNotFound)}
		resolver := NewResolver(cache, s1, s2)

		_, err := resolver.ListVersions(ctx, module)
		if err == nil {
			t.Fatal("ListVersions() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("error = %v, want ErrReleaseNotFound", err)
		}
	})
}
