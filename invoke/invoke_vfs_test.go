package invoke_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/runtime/tswasmer"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/vfs"
)

// TestVFSRoundTripThroughInvoke is an outside-in integration test that exercises
// the full tool execution path with the shared VFS:
//
//  1. Go pre-populates /input.txt in the MemFS
//  2. invoke.RunWithVFS loads the vfs-test tool package
//  3. The TS entry calls exec("vfs-guest", [])
//  4. invoke wires up the VFS server, passes the socket to wasmersandbox
//  5. The WASM guest reads /input.txt via ProxyFs and writes /output.txt
//  6. The TS entry returns the guest's stdout ("ok")
//  7. Go verifies /output.txt is visible in the MemFS with correct contents
func TestVFSRoundTripThroughInvoke(t *testing.T) {
	hostBinary := tswasmer.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(hostBinary); err != nil {
		t.Skipf("wasmersandbox binary not built: %v", err)
	}

	fixtureDir := vfsTestFixtureDir(t)
	if _, err := os.Stat(filepath.Join(fixtureDir, "dist", "vfs-guest.wasm")); err != nil {
		t.Skipf("vfs-guest.wasm not found: %v", err)
	}

	// Load the vfs-test tool package.
	loaded, err := packaging.LoadSourcePackage(fixtureDir)
	if err != nil {
		t.Fatalf("load vfs-test package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	// Pre-populate the VFS with input data.
	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("hello from go")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	// Run the tool through invoke — this exercises the full path:
	// TS (quickts) → exec host import → tswasmer → wasmersandbox → ProxyFs → VFS server
	result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{}, memFS)
	if err != nil {
		t.Fatalf("invoke.RunWithVFS: %v", err)
	}

	// The TS entry returns the WASM guest's stdout.
	if result != "ok" {
		t.Fatalf("expected tool result 'ok', got %q", result)
	}

	// The WASM guest wrote /output.txt — verify it from the Go side.
	output, err := memFS.ReadAll("/output.txt")
	if err != nil {
		t.Fatalf("ReadAll /output.txt: %v", err)
	}
	if string(output) != "got: hello from go" {
		t.Fatalf("expected 'got: hello from go', got %q", string(output))
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
