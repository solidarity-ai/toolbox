package invoke_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/vfs"
)

func TestVFSRoundTripThroughInvoke(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)

	loaded, err := packaging.LoadDev(vfsTestFixtureDir(t))
	if err != nil {
		t.Fatalf("load vfs-test package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("hello from go")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}

	if result != "got: hello from go" {
		t.Fatalf("expected 'got: hello from go', got %q", result)
	}
}

func TestVFSRoundTripThroughInvokeFromDistArchive(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)

	resolved := tooltest.VFSTestDistToolset(t)

	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("hello from go via dist")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}

	if result != "got: hello from go via dist" {
		t.Fatalf("expected 'got: hello from go via dist', got %q", result)
	}
}

func TestVFSDeleteThroughInvokeWasix(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)

	loaded, err := packaging.LoadDev(vfsTestFixtureDir(t))
	if err != nil {
		t.Fatalf("load vfs-test package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("delete me")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{"action": "delete"}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}
	if result != "deleted" {
		t.Fatalf("expected 'deleted', got %q", result)
	}
	if _, err := memFS.ReadAll("/input.txt"); err == nil {
		t.Fatal("expected /input.txt to be removed from MemFS")
	}
}

func TestVFSRenameThroughInvokeWasix(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)

	loaded, err := packaging.LoadDev(vfsTestFixtureDir(t))
	if err != nil {
		t.Fatalf("load vfs-test package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("rename me")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{"action": "rename"}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}
	if result != "renamed" {
		t.Fatalf("expected 'renamed', got %q", result)
	}
	if _, err := memFS.ReadAll("/input.txt"); err == nil {
		t.Fatal("expected /input.txt to be removed from MemFS")
	}
	renamed, err := memFS.ReadAll("/renamed.txt")
	if err != nil {
		t.Fatalf("ReadAll /renamed.txt: %v", err)
	}
	if string(renamed) != "rename me" {
		t.Fatalf("expected renamed file contents to survive, got %q", string(renamed))
	}
}

func TestVFSRoundTripThroughInvokeWasip2(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)

	loaded, err := packaging.LoadDev(vfsWasip2FixtureDir(t))
	if err != nil {
		t.Fatalf("load vfs-wasip2 package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("hello from go")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	result, err := invoke.RunWithVFS(resolved, "vfs-wasip2.run", map[string]any{}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}

	if result != "got: hello from go" {
		t.Fatalf("expected 'got: hello from go', got %q", result)
	}
}

func vfsWasip2FixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "vfs-wasip2")
}

func vfsTestFixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "vfs-test")
}
