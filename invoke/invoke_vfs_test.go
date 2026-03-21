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

// TestVFSRoundTripThroughInvoke is an outside-in integration test that exercises
// the full tool execution path with the shared VFS:
//
//  1. Go pre-populates /input.txt in the MemFS
//  2. invoke.RunWithVFS loads the vfs-test tool package
//  3. The TS entry calls exec("vfs-guest", []) — WASM guest reads
//     /work/input.txt via ProxyFs and writes /work/output.txt
//  4. The TS entry calls fs.readFileSync("/output.txt") to read the file
//     the WASM guest wrote, via the same shared MemFS
//  5. The TS entry returns the file contents as the tool result
//  6. Go verifies the result matches what the WASM guest wrote
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

// TestVFSRoundTripThroughInvokeFromDistArchive loads the vfs-test package from
// a dist archive and runs the full VFS round-trip, verifying the archive
// bundles WASM executables correctly.
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

func vfsTestFixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "vfs-test")
}
