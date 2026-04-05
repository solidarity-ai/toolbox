package tooltest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
)

func TestLocalPackageDeclUsesManifestModuleForExplicitDir(t *testing.T) {
	t.Parallel()

	srcDir := LocalSrcToolDir("calc")
	dstDir := filepath.Join(t.TempDir(), "renamed-checkout")
	copyDir(t, srcDir, dstDir)

	loadedPkgs, err := assembler.Load(context.Background(), nil, LocalPackageDecl(dstDir))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loadedPkgs.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(loadedPkgs.Packages))
	}
	if got := loadedPkgs.Packages[0].Package.Package.Module; got != fixtureModule("calc") {
		t.Fatalf("loaded module = %q, want %q", got, fixtureModule("calc"))
	}
}

func copyDir(t *testing.T, srcDir, dstDir string) {
	t.Helper()

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", srcDir, dstDir, err)
	}
}
