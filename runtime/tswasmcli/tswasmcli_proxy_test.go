package tswasmcli_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestWasip2CLIProxyRequestShapeStillRunsGuest(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireWasip2Artifacts(t)

	proxy, err := mitmproxy.New(nil)
	if err != nil {
		t.Fatalf("mitmproxy.New: %v", err)
	}
	listener, err := proxy.ListenAndServe()
	if err != nil {
		t.Fatalf("proxy.ListenAndServe: %v", err)
	}
	defer listener.Close()

	etcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(etcDir, "ssl", "certs"), 0o755); err != nil {
		t.Fatalf("mkdir ssl certs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etcDir, "hosts"), []byte("127.0.0.1 localhost proxy.toolbox.internal\n::1 localhost\n"), 0o644); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etcDir, "ssl", "certs", "ca-certificates.crt"), mitmproxy.CACertPEM(), 0o644); err != nil {
		t.Fatalf("write ca file: %v", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split proxy addr: %v", err)
	}

	result, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
		Runtime:  "wasip2-cli",
		Env: map[string]string{
			"HTTPS_PROXY":   "http://proxy.toolbox.internal:" + port,
			"HTTP_PROXY":    "http://proxy.toolbox.internal:" + port,
			"SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt",
			"NO_PROXY":      "",
			"no_proxy":      "",
		},
		Mounts: []tswasmcli.Mount{{
			HostPath:  etcDir,
			GuestPath: "/etc",
		}},
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
}

func TestWasip2CLIRejectsPartialProxyConfig(t *testing.T) {
	requireWasip2Artifacts(t)

	_, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
		Runtime:  "wasip2-cli",
		Env: map[string]string{
			"HTTPS_PROXY": "http://proxy.toolbox.internal:8443",
		},
	})
	if err == nil {
		t.Fatal("expected partial proxy config to fail")
	}
	if !strings.Contains(err.Error(), "requires both HTTPS_PROXY and SSL_CERT_FILE") {
		t.Fatalf("error = %v, want explicit proxy config validation", err)
	}
}

func TestWasip2CLIRejectsProxyConfigWithoutMountedTrustPath(t *testing.T) {
	requireWasip2Artifacts(t)

	_, err := tswasmcli.Run(tswasmcli.Request{
		WasmPath: filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
		Runtime:  "wasip2-cli",
		Env: map[string]string{
			"HTTPS_PROXY":   "http://proxy.toolbox.internal:8443",
			"SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt",
		},
	})
	if err == nil {
		t.Fatal("expected unmapped trust path to fail")
	}
	if !strings.Contains(err.Error(), "mounted guest path for SSL_CERT_FILE") {
		t.Fatalf("error = %v, want mounted trust path validation", err)
	}
}
