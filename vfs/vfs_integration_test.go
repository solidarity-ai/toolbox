package vfs_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasixcli"
	"github.com/solidarity-ai/toolbox/vfs"
)

// TestWASMGuestFileRoundTrip runs a real WASI guest binary that reads
// /work/input.txt from the VFS proxy and writes /work/output.txt back through
// it. Verifies both the guest stdout and the written file from the Go side.
func TestWASMGuestFileRoundTrip(t *testing.T) {
	wasmPath := fixtureWasmPath(t)
	hostBinary := tswasixcli.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(hostBinary); err != nil {
		t.Skipf("wasixcli-sandbox binary not built: %v", err)
	}
	if _, err := os.Stat(wasmPath); err != nil {
		t.Skipf("vfs-guest.wasm not found: %v", err)
	}

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

	result, err := tswasixcli.Run(tswasixcli.Request{
		WasmPath:    wasmPath,
		VFSSockPath: sockPath,
	})
	if err != nil {
		t.Fatalf("tswasixcli.Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("guest exited %d: stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if result.Stdout != "ok" {
		t.Fatalf("expected stdout 'ok', got %q", result.Stdout)
	}

	// Verify the guest wrote /output.txt visible from the Go side.
	output, err := memFS.ReadAll("/output.txt")
	if err != nil {
		t.Fatalf("ReadAll /output.txt: %v", err)
	}
	if string(output) != "got: hello from go" {
		t.Fatalf("expected 'got: hello from go', got %q", string(output))
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
