package invoke_test

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/runtime/tswasixcli"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/vfs"
	"github.com/vmihailenco/msgpack/v5"
)

// requireDeps fails the benchmark if the wasixcli-sandbox binary or
// vfs-guest.wasm fixture is not available.
func requireDeps(b *testing.B) string {
	b.Helper()
	hostBinary := tswasixcli.ResolveHostBinaryPathForTest()
	if _, err := os.Stat(hostBinary); err != nil {
		b.Fatalf("wasixcli-sandbox binary not built: %v", err)
	}
	fixtureDir := benchFixtureDir(b)
	wasmPath := filepath.Join(fixtureDir, "dist", "vfs-guest.wasm")
	if _, err := os.Stat(wasmPath); err != nil {
		b.Fatalf("vfs-guest.wasm not found: %v", err)
	}
	return fixtureDir
}

func benchFixtureDir(b *testing.B) string {
	b.Helper()
	// invoke_bench_test.go is in invoke/, fixture is relative to repo root.
	return filepath.Join(mustRepoRoot(b), "testutil", "fixtures", "toolbox.pkgs", "vfs-test")
}

func mustRepoRoot(b *testing.B) string {
	b.Helper()
	// Walk up from the test file's directory to find go.mod.
	dir, err := os.Getwd()
	if err != nil {
		b.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			b.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

// BenchmarkVFSRoundTrip contains sub-benchmarks that measure different segments
// of the invoke → TS → WASM → VFS pipeline.
func BenchmarkVFSRoundTrip(b *testing.B) {
	fixtureDir := requireDeps(b)

	b.Run("VFSServerOnly", func(b *testing.B) {
		benchVFSServerOnly(b)
	})

	b.Run("WASMGuestOnly", func(b *testing.B) {
		benchWASMGuestOnly(b, fixtureDir)
	})

	b.Run("FullRoundTrip", func(b *testing.B) {
		benchFullRoundTrip(b, fixtureDir)
	})
}

// benchVFSServerOnly measures VFS UDS server overhead: open + write + seek +
// read + close for a single file, exercised purely over the Unix socket.
func benchVFSServerOnly(b *testing.B) {
	memFS := vfs.NewMemFS()
	sockPath := filepath.Join(b.TempDir(), "vfs.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	srv := vfs.NewServer(memFS, listener)
	go srv.Serve()
	b.Cleanup(func() { srv.Close() })

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	b.Cleanup(func() { conn.Close() })

	payload := []byte("hello from benchmark")

	b.ResetTimer()
	for range b.N {
		// Write a file via the proxy.
		writeReq(b, conn, vfs.Request{
			Op:       vfs.OpOpen,
			Path:     "/bench.txt",
			OpenOpts: &vfs.OpenOpts{Read: true, Write: true, Create: true, Truncate: true},
		})
		resp := readResp(b, conn)
		handle := resp.Handle

		writeReq(b, conn, vfs.Request{Op: vfs.OpFileWrite, Handle: handle, Data: payload})
		readResp(b, conn)

		writeReq(b, conn, vfs.Request{Op: vfs.OpFileSeek, Handle: handle, SeekFrom: 0, SeekPos: 0})
		readResp(b, conn)

		writeReq(b, conn, vfs.Request{Op: vfs.OpFileRead, Handle: handle, Len: 4096})
		resp = readResp(b, conn)
		if string(resp.Data) != string(payload) {
			b.Fatalf("read mismatch: %q", resp.Data)
		}

		writeReq(b, conn, vfs.Request{Op: vfs.OpFileClose, Handle: handle})
		readResp(b, conn)
	}
}

// benchWASMGuestOnly measures VFS server + WASM guest execution (Rust via
// wasixcli-sandbox), bypassing the TS/QuickJS layer.
func benchWASMGuestOnly(b *testing.B, fixtureDir string) {
	wasmPath := filepath.Join(fixtureDir, "dist", "vfs-guest.wasm")

	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		memFS := vfs.NewMemFS()
		if err := memFS.WriteFile("/input.txt", []byte("hello from bench")); err != nil {
			b.Fatalf("pre-populate: %v", err)
		}
		sockPath := filepath.Join(b.TempDir(), "vfs.sock")
		listener, err := net.Listen("unix", sockPath)
		if err != nil {
			b.Fatalf("listen: %v", err)
		}
		srv := vfs.NewServer(memFS, listener)
		go srv.Serve()
		b.StartTimer()

		result, err := tswasixcli.Run(tswasixcli.Request{
			WasmPath:    wasmPath,
			VFSSockPath: sockPath,
		})

		b.StopTimer()
		srv.Close()
		os.Remove(sockPath)
		b.StartTimer()

		if err != nil {
			b.Fatalf("tswasixcli.Run: %v", err)
		}
		if result.ExitCode != 0 {
			b.Fatalf("guest exited %d: stderr=%q", result.ExitCode, result.Stderr)
		}
	}
}

// benchFullRoundTrip measures the complete invoke.RunWithVFS() path:
// Go → TS (QuickJS) → exec → wasixcli-sandbox → ProxyFs → VFS server,
// then TS reads the written file back via fs.readFileSync → MemFS.
func benchFullRoundTrip(b *testing.B, fixtureDir string) {
	loaded, err := packaging.LoadSourcePackage(fixtureDir)
	if err != nil {
		b.Fatalf("load vfs-test package: %v", err)
	}
	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	b.ResetTimer()
	for range b.N {
		memFS := vfs.NewMemFS()
		if err := memFS.WriteFile("/input.txt", []byte("hello from bench")); err != nil {
			b.Fatalf("pre-populate: %v", err)
		}

		result, err := invoke.RunWithVFS(resolved, "vfs-test.run", map[string]any{}, memFS)
		if err != nil {
			b.Fatalf("RunWithVFS: %v", err)
		}
		if result != "got: hello from bench" {
			b.Fatalf("expected 'got: hello from bench', got %q", result)
		}
	}
}

// writeReq sends a framed msgpack request over the connection (same wire
// format as vfs.writeFrame, which is unexported).
func writeReq(b *testing.B, conn net.Conn, req vfs.Request) {
	b.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		b.Fatalf("marshal request: %v", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		b.Fatalf("write len: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		b.Fatalf("write data: %v", err)
	}
}

// readResp reads a framed msgpack response from the connection.
func readResp(b *testing.B, conn net.Conn) vfs.Response {
	b.Helper()
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		b.Fatalf("read len: %v", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		b.Fatalf("read data: %v", err)
	}
	var resp vfs.Response
	if err := msgpack.Unmarshal(buf, &resp); err != nil {
		b.Fatalf("unmarshal response: %v", err)
	}
	return resp
}
