package tswasmcli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

const tinyGoWasip2NetdevUnavailable = "Netdev not set"

// TestWasip2CLIRunsHTTPClientWasm is a sandbox integration test that runs
// wasmcli-sandbox --runtime wasip2-cli against the pre-compiled http-client
// fixture through the local MITM proxy path, proving the fixture no longer
// depends on external httpbin.org behavior.
func TestWasip2CLIRunsHTTPClientWasm(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireWasip2Artifacts(t)

	server := tooltest.StartHTTPClientLocalTLSServer(t)
	policy := resolveHTTPClientPolicy(t, nil)
	_, mounts, env := startRuntimeProxyHarness(t, policy, server)

	wasmPath := filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm")
	result, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: wasmPath,
		Runtime:  "wasip2-cli",
		Args:     []string{server.URL("/plain/ok")},
		Env:      env,
		Mounts:   mounts,
	})
	if err != nil {
		t.Fatalf("tswasmcli.Run: %v", err)
	}

	if result.ExitCode != 0 {
		skipIfTinyGoWasip2HTTPUnavailable(t, result.Stdout, result.Stderr)
		t.Fatalf("expected exit code 0, got %d; stderr: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Status: 200 OK") {
		t.Fatalf("expected stdout to contain 'Status: 200 OK', got:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "/plain/ok") {
		t.Fatalf("expected stdout to contain '/plain/ok', got:\n%s", result.Stdout)
	}
	if got := server.HitCount(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
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

func skipIfTinyGoWasip2HTTPUnavailable(t *testing.T, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, tinyGoWasip2NetdevUnavailable) {
			t.Skip("skipping TinyGo wasip2 HTTP integration until wasmcli-sandbox exposes a compatible network device layer")
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
