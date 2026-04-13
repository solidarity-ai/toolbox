package assembler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestLoadLocalPackageDeclLoadsPackageName(t *testing.T) {
	t.Parallel()

	loadedPkgs, err := assembler.Load(context.Background(), nil, tooltest.LocalPackageDecl("calc"))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loadedPkgs.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(loadedPkgs.Packages))
	}
	if loadedPkgs.Packages[0].Package.Package.Name != "calc" {
		t.Fatalf("expected package name %q, got %q", "calc", loadedPkgs.Packages[0].Package.Package.Name)
	}
}

func TestLoadRegistry(t *testing.T) {
	t.Parallel()

	module := tooldef.ModulePath("fixtures.local/calc")
	version := tooldef.Version("v1.2.3")
	ctx := context.Background()
	decl := assembler.Declaration{
		Packages: []assembler.PackageDeclaration{{
			Module:  module,
			Version: version,
		}},
	}

	t.Run("NilResolverReturnsErrNoResolver", func(t *testing.T) {
		_, err := assembler.Load(ctx, nil, decl)
		if err == nil {
			t.Fatal("Load() error = nil, want ErrNoResolver")
		}
		if !errors.Is(err, assembler.ErrNoResolver) {
			t.Fatalf("error = %v, want ErrNoResolver", err)
		}
	})

	t.Run("LoadsFromPrePopulatedCache", func(t *testing.T) {
		archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath(module), registry.Version(version), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		resolver := registry.NewResolver(cache)
		loadedPkgs, err := assembler.Load(ctx, resolver, decl)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if len(loadedPkgs.Packages) != 1 {
			t.Fatalf("expected 1 package, got %d", len(loadedPkgs.Packages))
		}
		if loadedPkgs.Packages[0].Package.Package.Name != "calc" {
			t.Fatalf("package name = %q, want %q", loadedPkgs.Packages[0].Package.Package.Name, "calc")
		}
		if loadedPkgs.Packages[0].Metadata == nil {
			t.Fatal("expected registry metadata to be recorded")
		}
	})

	t.Run("LoadedPackageAppearsInPrepareTools", func(t *testing.T) {
		archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath(module), registry.Version(version), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}

		resolver := registry.NewResolver(cache)
		loadedPkgs, err := assembler.Load(ctx, resolver, decl)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		prepared, err := toolset.PrepareTools(context.Background(), loadedPkgs.Tools(), toolset.Config{})
		if err != nil {
			t.Fatalf("PrepareTools() error: %v", err)
		}
		if len(prepared.Tools()) == 0 {
			t.Fatal("expected at least one prepared tool, got none")
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

	fixtureDir := tooltest.PackageDistToolDir(fixtureName)
	archivePath := filepath.Join(fixtureDir, fixtureName+".toolbox.pkg")
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
