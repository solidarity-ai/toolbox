package toolset

import (
	"path/filepath"
	"testing"
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
