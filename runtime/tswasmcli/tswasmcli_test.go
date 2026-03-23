package tswasmcli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
)

// TestWasip2CLIRunsHTTPClientWasm is a sandbox integration test that runs
// wasmcli-sandbox --runtime wasip2-cli directly against the pre-compiled
// http-client.wasm fixture. The WASM binary does http.Get("https://httpbin.org/get")
// and prints the status + body to stdout.
func TestWasip2CLIRunsHTTPClientWasm(t *testing.T) {
	requireWasip2Artifacts(t)

	wasmPath := filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm")
	result, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: wasmPath,
		Runtime:  "wasip2-cli",
	})
	if err != nil {
		t.Fatalf("tswasmcli.Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", result.ExitCode, result.Stderr)
	}

	if !strings.Contains(result.Stdout, "Status: 200 OK") {
		t.Fatalf("expected stdout to contain 'Status: 200 OK', got:\n%s", result.Stdout)
	}

	if !strings.Contains(result.Stdout, "httpbin.org") {
		t.Fatalf("expected stdout to contain 'httpbin.org', got:\n%s", result.Stdout)
	}
}

func TestRunRejectsMissingRuntime(t *testing.T) {
	_, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: "/dummy.wasm",
	})
	if err == nil {
		t.Fatal("expected error for missing runtime")
	}
	if !strings.Contains(err.Error(), "missing runtime") {
		t.Fatalf("expected 'missing runtime' error, got: %v", err)
	}
}

func requireWasip2Artifacts(t *testing.T) {
	t.Helper()

	paths := []string{
		tswasmcli.ResolveHostBinaryPathForTest(),
		filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("wasip2 artifacts not ready: missing %s — rebuild with: cargo build --manifest-path wasmcli-sandbox/Cargo.toml", p)
		}
	}
}

func httpClientFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tswasmcli_test: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testutil", "fixtures", "toolbox.pkgs", "http-client")
}
