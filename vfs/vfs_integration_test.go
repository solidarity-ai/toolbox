package vfs_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmer"
	"github.com/solidarity-ai/toolbox/vfs"
)

// TestWASMGuestReadsFromVFS is an end-to-end integration test that:
//  1. Starts a Go VFS server and pre-populates /input.txt
//  2. Runs a real WASI guest binary (vfs-guest.wasm) via wasmersandbox
//  3. The guest reads /input.txt from the VFS proxy and prints the contents
//  4. Verifies the guest stdout contains the expected data
//
// The guest binary was compiled from wasm-src/main.rs with:
//
//	cargo build --release --target wasm32-wasip1
func TestWASMGuestReadsFromVFS(t *testing.T) {
	wasmPath := fixtureWasmPath(t)
	hostBinary := tswasmer.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(hostBinary); err != nil {
		t.Skipf("wasmersandbox binary not built: %v", err)
	}
	if _, err := os.Stat(wasmPath); err != nil {
		t.Skipf("vfs-guest.wasm not found: %v", err)
	}

	// Start VFS server and pre-populate.
	memFS := vfs.NewMemFS()
	if err := memFS.WriteFile("/input.txt", []byte("hello from go")); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	sockPath := filepath.Join(t.TempDir(), "vfs.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := vfs.NewServer(memFS, listener)
	go srv.Serve()
	t.Cleanup(func() { srv.Close() })

	// Run the WASM guest.
	result, err := tswasmer.Run(tswasmer.Request{
		WasmPath:    wasmPath,
		VFSSockPath: sockPath,
	})
	if err != nil {
		t.Fatalf("tswasmer.Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("guest exited %d: stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}

	// The guest reads /input.txt and prints "got: <contents>" to stdout.
	if result.Stdout != "got: hello from go" {
		t.Fatalf("expected stdout 'got: hello from go', got %q", result.Stdout)
	}
}

func fixtureWasmPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "vfs-test", "dist", "vfs-guest.wasm")
}
