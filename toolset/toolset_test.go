package toolset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
)

func TestBuilderAddFromDirLoadsPackageName(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	pkgs := ts.Packages()
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Name != "calc" {
		t.Fatalf("expected package name %q, got %q", "calc", pkgs[0].Name)
	}
}

func TestBuilderAddFromRegistry(t *testing.T) {
	t.Parallel()

	module := "example.com/acme/calc"
	version := "v1.2.3"
	ctx := context.Background()

	t.Run("NilResolverReturnsErrNoResolver", func(t *testing.T) {
		b := New()
		err := b.AddFromRegistry(ctx, module, version)
		if err == nil {
			t.Fatal("AddFromRegistry() error = nil, want ErrNoResolver")
		}
		if !errors.Is(err, ErrNoResolver) {
			t.Fatalf("error = %v, want ErrNoResolver", err)
		}
	})

	t.Run("InvalidModulePathReturnsError", func(t *testing.T) {
		cache := newTempCache(t)
		resolver := registry.NewResolver(cache)
		b := NewWithResolver(resolver)

		err := b.AddFromRegistry(ctx, "bad", version)
		if err == nil {
			t.Fatal("AddFromRegistry() error = nil, want parse error")
		}
	})

	t.Run("InvalidVersionReturnsError", func(t *testing.T) {
		cache := newTempCache(t)
		resolver := registry.NewResolver(cache)
		b := NewWithResolver(resolver)

		err := b.AddFromRegistry(ctx, module, "notaversion")
		if err == nil {
			t.Fatal("AddFromRegistry() error = nil, want parse error")
		}
	})

	t.Run("ResolvesFromPrePopulatedCache", func(t *testing.T) {
		archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath(module), registry.Version(version), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		resolver := registry.NewResolver(cache)
		b := NewWithResolver(resolver)

		if err := b.AddFromRegistry(ctx, module, version); err != nil {
			t.Fatalf("AddFromRegistry() error: %v", err)
		}

		pkgs := b.Packages()
		if len(pkgs) != 1 {
			t.Fatalf("expected 1 package, got %d", len(pkgs))
		}
		if pkgs[0].Name != "calc" {
			t.Fatalf("package name = %q, want %q", pkgs[0].Name, "calc")
		}
	})

	t.Run("ResolvedPackageAppearsInResolve", func(t *testing.T) {
		archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath(module), registry.Version(version), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		resolver := registry.NewResolver(cache)
		b := NewWithResolver(resolver)

		if err := b.AddFromRegistry(ctx, module, version); err != nil {
			t.Fatalf("AddFromRegistry() error: %v", err)
		}

		resolved := b.Resolve()
		tools := resolved.Tools()
		if len(tools) == 0 {
			t.Fatal("expected at least one resolved tool, got none")
		}
	})
}

func newTempCache(t *testing.T) *registry.Cache {
	t.Helper()
	cache, err := registry.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache() error: %v", err)
	}
	return cache
}

func loadDistFixtureBytes(t *testing.T, fixtureName string) ([]byte, []byte) {
	t.Helper()

	fixtureDir := ""
	for _, dir := range fixtures.DistDirs() {
		if filepath.Base(dir) == fixtureName {
			fixtureDir = dir
			break
		}
	}
	if fixtureDir == "" {
		t.Fatalf("dist fixture %q not found", fixtureName)
	}

	archivePath := filepath.Join(fixtureDir, "calc.toolbox.pkg")
	manifestPath := filepath.Join(fixtureDir, packaging.PkgManifestFilename)

	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive fixture %s: %v", archivePath, err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", manifestPath, err)
	}
	return archiveBytes, manifestBytes
}
