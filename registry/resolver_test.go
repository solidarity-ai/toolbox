package registry

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// mockSource is a test PackageSource that returns preconfigured results.
type mockSource struct {
	archive  []byte
	manifest []byte
	err      error
	called   bool
}

func (m *mockSource) Fetch(_ context.Context, _ ModulePath, _ Version) ([]byte, []byte, error) {
	m.called = true
	return m.archive, m.manifest, m.err
}

func TestResolver(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	module := ModulePath("example.com/acme/calc")
	version := Version("v1.2.3")
	ctx := context.Background()

	t.Run("CacheHitBypassesSources", func(t *testing.T) {
		cache := newTempCache(t)
		if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		src := &mockSource{err: fmt.Errorf("should not be called")}
		resolver := NewResolver(cache, src)

		pkg, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if pkg.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", pkg.Package.Name, "calc")
		}
		if src.called {
			t.Fatal("source was called despite cache hit")
		}
	})

	t.Run("PrimarySourceSuccessPopulatesCache", func(t *testing.T) {
		cache := newTempCache(t)
		src := &mockSource{archive: archiveBytes, manifest: manifestBytes}
		resolver := NewResolver(cache, src)

		pkg, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if pkg.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", pkg.Package.Name, "calc")
		}
		if !src.called {
			t.Fatal("source was not called")
		}
		if !cache.Has(module, version) {
			t.Fatal("cache.Has() = false after successful resolve, want true")
		}
	})

	t.Run("FallbackOnErrReleaseNotFound", func(t *testing.T) {
		cache := newTempCache(t)
		primary := &mockSource{err: fmt.Errorf("primary: %w", ErrReleaseNotFound)}
		fallback := &mockSource{archive: archiveBytes, manifest: manifestBytes}
		resolver := NewResolver(cache, primary, fallback)

		pkg, err := resolver.Resolve(ctx, module, version)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if pkg.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", pkg.Package.Name, "calc")
		}
		if !primary.called {
			t.Fatal("primary source was not called")
		}
		if !fallback.called {
			t.Fatal("fallback source was not called")
		}
	})

	t.Run("NoFallbackOnNon404Error", func(t *testing.T) {
		cache := newTempCache(t)
		primary := &mockSource{err: fmt.Errorf("network timeout")}
		fallback := &mockSource{archive: archiveBytes, manifest: manifestBytes}
		resolver := NewResolver(cache, primary, fallback)

		_, err := resolver.Resolve(ctx, module, version)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if !primary.called {
			t.Fatal("primary source was not called")
		}
		if fallback.called {
			t.Fatal("fallback source was called despite non-404 primary error")
		}
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
		if !s1.called || !s2.called {
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
