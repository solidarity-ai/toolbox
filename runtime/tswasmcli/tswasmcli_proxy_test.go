package tswasmcli_test

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/transport"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestWasip2CLIProxyAllowsInjectedRequestsWithoutLeakingSecrets(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireWasip2Artifacts(t)

	tests := []struct {
		name           string
		path           string
		secretName     string
		secretValue    string
		assertUpstream func(t *testing.T, req tooltest.HTTPClientObservedRequest)
	}{
		{
			name:        "bearer header",
			path:        "/bearer/ok",
			secretName:  "bearer_token",
			secretValue: "bearer-secret-token",
			assertUpstream: func(t *testing.T, req tooltest.HTTPClientObservedRequest) {
				t.Helper()
				if got := req.Header.Get("Authorization"); got != "Bearer bearer-secret-token" {
					t.Fatalf("authorization = %q, want bearer token", got)
				}
			},
		},
		{
			name:        "basic auth",
			path:        "/basic/ok",
			secretName:  "basic_auth",
			secretValue: "aladdin:open-sesame",
			assertUpstream: func(t *testing.T, req tooltest.HTTPClientObservedRequest) {
				t.Helper()
				if got := req.Header.Get("Authorization"); got == "" || !strings.HasPrefix(got, "Basic ") {
					t.Fatalf("authorization = %q, want basic auth", got)
				}
			},
		},
		{
			name:        "api key header",
			path:        "/header/ok",
			secretName:  "header_key",
			secretValue: "header-secret-token",
			assertUpstream: func(t *testing.T, req tooltest.HTTPClientObservedRequest) {
				t.Helper()
				if got := req.Header.Get("X-Api-Key"); got != "header-secret-token" {
					t.Fatalf("X-API-Key = %q, want injected secret", got)
				}
			},
		},
		{
			name:        "api key query",
			path:        "/query/ok",
			secretName:  "query_key",
			secretValue: "query-secret-token",
			assertUpstream: func(t *testing.T, req tooltest.HTTPClientObservedRequest) {
				t.Helper()
				if !strings.Contains(req.Query, "api_key=query-secret-token") {
					t.Fatalf("query = %q, want injected api_key", req.Query)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := tooltest.StartHTTPClientLocalTLSServer(t)
			policy := resolveHTTPClientPolicy(t, map[string]string{tt.secretName: tt.secretValue})
			proxyAddr, mounts, env := startRuntimeProxyHarness(t, policy, server)
			_ = proxyAddr

			result, err := tswasmcli.Run(tswasmcli.Request{
				WasmPath: filepath.Join(tooltest.HTTPClientFixtureDir(t), "dist", "http-client.wasm"),
				Runtime:  "wasip2-cli",
				Args:     []string{server.URL(tt.path)},
				Env:      env,
				Mounts:   mounts,
			})
			if err != nil {
				t.Fatalf("tswasmcli.Run: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("expected exit code 0, got %d; stderr: %s", result.ExitCode, result.Stderr)
			}
			if got := server.HitCount(); got != 1 {
				t.Fatalf("upstream hits = %d, want 1", got)
			}
			if !strings.Contains(result.Stdout, "Status: 200 OK") {
				t.Fatalf("stdout = %q, want status line", result.Stdout)
			}
			if !strings.Contains(result.Stdout, tt.path) {
				t.Fatalf("stdout = %q, want response path %q", result.Stdout, tt.path)
			}
			if strings.Contains(result.Stdout, tt.secretValue) || strings.Contains(result.Stderr, tt.secretValue) {
				t.Fatalf("tool-visible output leaked secret %q; stdout=%q stderr=%q", tt.secretValue, result.Stdout, result.Stderr)
			}

			req := server.LastRequest(t)
			if req.Path != tt.path {
				t.Fatalf("upstream path = %q, want %q", req.Path, tt.path)
			}
			tt.assertUpstream(t, req)
		})
	}
}

func TestWasip2CLIProxyDeniedAndMissingSecretsFailClosed(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireWasip2Artifacts(t)

	tests := []struct {
		name          string
		url           string
		secrets       map[string]string
		wantErrSubstr string
		forbidSecret  string
	}{
		{
			name:          "denied host never reaches upstream",
			url:           "https://127.0.0.2:4443/query/blocked",
			secrets:       map[string]string{"query_key": "query-secret-token"},
			wantErrSubstr: `transport denied request to host "127.0.0.2": not allowed by policy`,
			forbidSecret:  "query-secret-token",
		},
		{
			name:          "missing secret surfaces explicit transport failure",
			url:           "https://127.0.0.1:4443/bearer/missing-secret",
			secrets:       nil,
			wantErrSubstr: `no secret store is available`,
			forbidSecret:  "bearer-secret-token",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := tooltest.StartHTTPClientLocalTLSServer(t)
			policy := resolveHTTPClientPolicy(t, tt.secrets)
			_, mounts, env := startRuntimeProxyHarness(t, policy, server)

			result, err := tswasmcli.Run(tswasmcli.Request{
				WasmPath: filepath.Join(tooltest.HTTPClientFixtureDir(t), "dist", "http-client.wasm"),
				Runtime:  "wasip2-cli",
				Args:     []string{tt.url},
				Env:      env,
				Mounts:   mounts,
			})
			if err != nil {
				t.Fatalf("tswasmcli.Run: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("expected non-zero exit code; stdout=%q stderr=%q", result.Stdout, result.Stderr)
			}
			combined := result.Stdout + "\n" + result.Stderr
			if !strings.Contains(combined, tt.wantErrSubstr) {
				t.Fatalf("combined output = %q, want %q", combined, tt.wantErrSubstr)
			}
			if tt.forbidSecret != "" && strings.Contains(combined, tt.forbidSecret) {
				t.Fatalf("combined output leaked secret %q: %q", tt.forbidSecret, combined)
			}
			if got := server.HitCount(); got != 0 {
				t.Fatalf("upstream hits = %d, want 0", got)
			}
		})
	}
}

func resolveHTTPClientPolicy(t *testing.T, secretValues map[string]string) *transport.Policy {
	t.Helper()
	store := testutil.NewTestSecretStore()
	for name, value := range secretValues {
		store.SeedStrings(map[string]string{tooltest.HTTPClientSecretKey(t, name): value})
	}
	resolved := tooltest.ResolveHTTPClientToolset(t, store)
	policy, ok := resolved.ToolTransportPolicy(tooltest.HTTPClientToolName)
	if !ok || policy == nil {
		t.Fatal("expected http-client transport policy")
	}
	return policy
}

func startRuntimeProxyHarness(t *testing.T, policy *transport.Policy, server *tooltest.HTTPClientLocalTLSServer) (string, []tswasmcli.Mount, map[string]string) {
	t.Helper()
	proxy, err := mitmproxy.New(nil)
	if err != nil {
		t.Fatalf("mitmproxy.New: %v", err)
	}
	proxy.Policy = policy
	proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}

	listener, err := proxy.ListenAndServe()
	if err != nil {
		t.Fatalf("proxy.ListenAndServe: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

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

	return listener.Addr().String(), []tswasmcli.Mount{{
			HostPath:  etcDir,
			GuestPath: "/etc",
		}}, map[string]string{
			"HTTPS_PROXY":   "http://proxy.toolbox.internal:" + port,
			"HTTP_PROXY":    "http://proxy.toolbox.internal:" + port,
			"SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt",
			"NO_PROXY":      "",
			"no_proxy":      "",
		}
}
