package vfs

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// client is a thin test helper that sends requests over UDS and reads responses.
type client struct {
	conn net.Conn
}

func dialServer(t *testing.T, sockPath string) *client {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial vfs server: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{conn: conn}
}

func (c *client) call(t *testing.T, req Request) Response {
	t.Helper()
	if err := writeFrame(c.conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := readFrame[Response](c.conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp
}

func startTestServer(t *testing.T) (string, *MemFS) {
	t.Helper()
	memFS := NewMemFS()
	sockPath := filepath.Join(t.TempDir(), "vfs.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := NewServer(memFS, listener)
	go srv.Serve()
	t.Cleanup(func() {
		srv.Close()
		os.Remove(sockPath)
	})

	return sockPath, memFS
}

func TestCreateDirAndReadDir(t *testing.T) {
	sockPath, _ := startTestServer(t)
	c := dialServer(t, sockPath)

	// Create /foo.
	resp := c.call(t, Request{Op: OpCreateDir, Path: "/foo"})
	if resp.Err != ErrOK {
		t.Fatalf("create_dir /foo: err=%d", resp.Err)
	}

	// Read root.
	resp = c.call(t, Request{Op: OpReadDir, Path: "/"})
	if resp.Err != ErrOK {
		t.Fatalf("read_dir /: err=%d", resp.Err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "foo" {
		t.Fatalf("expected [foo], got %v", resp.Entries)
	}
	if !resp.Entries[0].Meta.IsDir {
		t.Fatalf("expected foo to be a dir")
	}
}

func TestOpenWriteReadSeek(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	// Open a new file for writing.
	resp := c.call(t, Request{
		Op:   OpOpen,
		Path: "/hello.txt",
		OpenOpts: &OpenOpts{
			Read:   true,
			Write:  true,
			Create: true,
		},
	})
	if resp.Err != ErrOK {
		t.Fatalf("open: err=%d", resp.Err)
	}
	handle := resp.Handle

	// Write data.
	resp = c.call(t, Request{Op: OpFileWrite, Handle: handle, Data: []byte("hello world")})
	if resp.Err != ErrOK {
		t.Fatalf("write: err=%d", resp.Err)
	}
	if resp.N != 11 {
		t.Fatalf("expected 11 bytes written, got %d", resp.N)
	}

	// Seek back to start.
	resp = c.call(t, Request{Op: OpFileSeek, Handle: handle, SeekFrom: 0, SeekPos: 0})
	if resp.Err != ErrOK {
		t.Fatalf("seek: err=%d", resp.Err)
	}

	// Read it back.
	resp = c.call(t, Request{Op: OpFileRead, Handle: handle, Len: 100})
	if resp.Err != ErrOK {
		t.Fatalf("read: err=%d", resp.Err)
	}
	if string(resp.Data) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", string(resp.Data))
	}

	// Close.
	resp = c.call(t, Request{Op: OpFileClose, Handle: handle})
	if resp.Err != ErrOK {
		t.Fatalf("close: err=%d", resp.Err)
	}

	// Verify via MemFS directly.
	data, err := memFS.ReadAll("/hello.txt")
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("memfs data mismatch: %q", string(data))
	}
}

func TestMetadata(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	if err := memFS.WriteFile("/test.txt", []byte("abc")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp := c.call(t, Request{Op: OpMetadata, Path: "/test.txt"})
	if resp.Err != ErrOK {
		t.Fatalf("metadata: err=%d", resp.Err)
	}
	if resp.Meta == nil {
		t.Fatal("expected metadata")
	}
	if !resp.Meta.IsFile {
		t.Fatal("expected file")
	}
	if resp.Meta.Len != 3 {
		t.Fatalf("expected len=3, got %d", resp.Meta.Len)
	}
}

func TestRemoveFile(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	if err := memFS.WriteFile("/rm.txt", []byte("bye")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp := c.call(t, Request{Op: OpRemoveFile, Path: "/rm.txt"})
	if resp.Err != ErrOK {
		t.Fatalf("remove_file: err=%d", resp.Err)
	}

	// Should be gone.
	resp = c.call(t, Request{Op: OpMetadata, Path: "/rm.txt"})
	if resp.Err != ErrNotFound {
		t.Fatalf("expected not found, got err=%d", resp.Err)
	}
}

func TestRename(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	if err := memFS.WriteFile("/a.txt", []byte("data")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp := c.call(t, Request{Op: OpRename, Path: "/a.txt", ToPath: "/b.txt"})
	if resp.Err != ErrOK {
		t.Fatalf("rename: err=%d", resp.Err)
	}

	// Old name gone.
	resp = c.call(t, Request{Op: OpMetadata, Path: "/a.txt"})
	if resp.Err != ErrNotFound {
		t.Fatalf("expected not found for old name, got err=%d", resp.Err)
	}

	// New name exists.
	data, err := memFS.ReadAll("/b.txt")
	if err != nil {
		t.Fatalf("ReadAll /b.txt: %v", err)
	}
	if string(data) != "data" {
		t.Fatalf("expected 'data', got %q", string(data))
	}
}

func TestRemoveDir(t *testing.T) {
	sockPath, _ := startTestServer(t)
	c := dialServer(t, sockPath)

	// Create and then remove a dir.
	c.call(t, Request{Op: OpCreateDir, Path: "/d"})
	resp := c.call(t, Request{Op: OpRemoveDir, Path: "/d"})
	if resp.Err != ErrOK {
		t.Fatalf("remove_dir: err=%d", resp.Err)
	}

	// Should be gone.
	resp = c.call(t, Request{Op: OpMetadata, Path: "/d"})
	if resp.Err != ErrNotFound {
		t.Fatalf("expected not found, got err=%d", resp.Err)
	}
}

func TestErrorOnNotFound(t *testing.T) {
	sockPath, _ := startTestServer(t)
	c := dialServer(t, sockPath)

	resp := c.call(t, Request{Op: OpMetadata, Path: "/nonexistent"})
	if resp.Err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %d", resp.Err)
	}
}

// TestGuestWriteWorkflow simulates a WASM guest that creates a directory,
// writes files into it, reads them back over the proxy, and then verifies
// everything is visible from the Go MemFS side. This is the integration test
// for the full VFS round-trip — the same sequence of operations that the Rust
// ProxyFs would perform on behalf of a WASIX guest.
func TestGuestWriteWorkflow(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	// Step 1: Go side pre-populates a config file the guest will read.
	if err := memFS.WriteFile("/config.json", []byte(`{"key":"value"}`)); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	// Step 2: Guest reads the pre-populated file via proxy.
	resp := c.call(t, Request{
		Op:       OpOpen,
		Path:     "/config.json",
		OpenOpts: &OpenOpts{Read: true},
	})
	if resp.Err != ErrOK {
		t.Fatalf("open config: err=%d", resp.Err)
	}
	configHandle := resp.Handle

	resp = c.call(t, Request{Op: OpFileRead, Handle: configHandle, Len: 4096})
	if resp.Err != ErrOK {
		t.Fatalf("read config: err=%d", resp.Err)
	}
	if string(resp.Data) != `{"key":"value"}` {
		t.Fatalf("config mismatch: %q", string(resp.Data))
	}
	c.call(t, Request{Op: OpFileClose, Handle: configHandle})

	// Step 3: Guest creates an output directory.
	resp = c.call(t, Request{Op: OpCreateDir, Path: "/output"})
	if resp.Err != ErrOK {
		t.Fatalf("create /output: err=%d", resp.Err)
	}

	// Step 4: Guest writes two output files.
	for _, file := range []struct {
		path    string
		content string
	}{
		{"/output/result.json", `{"status":"ok","count":42}`},
		{"/output/log.txt", "line 1\nline 2\nline 3\n"},
	} {
		resp = c.call(t, Request{
			Op:       OpOpen,
			Path:     file.path,
			OpenOpts: &OpenOpts{Read: true, Write: true, Create: true},
		})
		if resp.Err != ErrOK {
			t.Fatalf("open %s: err=%d", file.path, resp.Err)
		}
		h := resp.Handle

		resp = c.call(t, Request{Op: OpFileWrite, Handle: h, Data: []byte(file.content)})
		if resp.Err != ErrOK {
			t.Fatalf("write %s: err=%d", file.path, resp.Err)
		}
		if resp.N != len(file.content) {
			t.Fatalf("write %s: expected %d bytes, got %d", file.path, len(file.content), resp.N)
		}

		c.call(t, Request{Op: OpFileClose, Handle: h})
	}

	// Step 5: Guest lists the output directory via proxy.
	resp = c.call(t, Request{Op: OpReadDir, Path: "/output"})
	if resp.Err != ErrOK {
		t.Fatalf("readdir /output: err=%d", resp.Err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	// Entries are sorted alphabetically.
	if resp.Entries[0].Name != "log.txt" || resp.Entries[1].Name != "result.json" {
		t.Fatalf("unexpected entries: %v, %v", resp.Entries[0].Name, resp.Entries[1].Name)
	}

	// Step 6: Guest reads result back via proxy to verify.
	resp = c.call(t, Request{
		Op:       OpOpen,
		Path:     "/output/result.json",
		OpenOpts: &OpenOpts{Read: true},
	})
	if resp.Err != ErrOK {
		t.Fatalf("reopen result: err=%d", resp.Err)
	}
	resultHandle := resp.Handle
	resp = c.call(t, Request{Op: OpFileRead, Handle: resultHandle, Len: 4096})
	if resp.Err != ErrOK {
		t.Fatalf("reread result: err=%d", resp.Err)
	}
	if string(resp.Data) != `{"status":"ok","count":42}` {
		t.Fatalf("result mismatch: %q", string(resp.Data))
	}
	c.call(t, Request{Op: OpFileClose, Handle: resultHandle})

	// Step 7: Verify from the Go side — this is what invoke would do after
	// WASM execution completes to harvest output files.
	resultData, err := memFS.ReadAll("/output/result.json")
	if err != nil {
		t.Fatalf("Go-side ReadAll result: %v", err)
	}
	if string(resultData) != `{"status":"ok","count":42}` {
		t.Fatalf("Go-side result mismatch: %q", string(resultData))
	}

	logData, err := memFS.ReadAll("/output/log.txt")
	if err != nil {
		t.Fatalf("Go-side ReadAll log: %v", err)
	}
	if string(logData) != "line 1\nline 2\nline 3\n" {
		t.Fatalf("Go-side log mismatch: %q", string(logData))
	}

	// Step 8: Verify the full directory listing from Go side.
	entries, err := memFS.ReadDir("/output")
	if err != nil {
		t.Fatalf("Go-side ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Go-side expected 2 entries, got %d", len(entries))
	}
}

func TestFileSetLen(t *testing.T) {
	sockPath, memFS := startTestServer(t)
	c := dialServer(t, sockPath)

	if err := memFS.WriteFile("/trunc.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp := c.call(t, Request{
		Op:       OpOpen,
		Path:     "/trunc.txt",
		OpenOpts: &OpenOpts{Read: true, Write: true},
	})
	if resp.Err != ErrOK {
		t.Fatalf("open: err=%d", resp.Err)
	}
	handle := resp.Handle

	resp = c.call(t, Request{Op: OpFileSetLen, Handle: handle, Len: 3})
	if resp.Err != ErrOK {
		t.Fatalf("set_len: err=%d", resp.Err)
	}

	c.call(t, Request{Op: OpFileClose, Handle: handle})

	data, err := memFS.ReadAll("/trunc.txt")
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "hel" {
		t.Fatalf("expected 'hel', got %q", string(data))
	}
}
