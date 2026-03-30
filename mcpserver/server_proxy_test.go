package mcpserver_test

import (
	"crypto/tls"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestMCPServerRunsWasip2PackageHTTPClientAllowedProxyRequests(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireTSWasip2Artifacts(t)

	tests := []struct {
		name           string
		path           string
		secretName     string
		secretValue    string
		assertUpstream func(t *testing.T, req tooltest.HTTPClientObservedRequest)
	}{
		{
			name:        "bearer header",
			path:        "/bearer/mcp",
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
			path:        "/basic/mcp",
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
			path:        "/header/mcp",
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
			path:        "/query/mcp",
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
			restore := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
				proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
			})
			defer restore()

			store := testutil.NewTestSecretStore()
			store.SeedStrings(map[string]string{tooltest.HTTPClientSecretKey(t, tt.secretName): tt.secretValue})
			resolved := tooltest.ResolveHTTPClientToolset(t, store)
			h := mcptest.NewHarness(t, mcpserver.New(resolved))

			result := h.CallTool(tooltest.HTTPClientToolName, map[string]any{"url": server.URL(tt.path)})
			if result.IsError {
				t.Fatalf("expected non-error result: %#v", result.Content)
			}
			text := requireSingleTextContent(t, result)
			if !strings.Contains(text, "Status: 200 OK") {
				t.Fatalf("result text = %q, want status line", text)
			}
			if !strings.Contains(text, tt.path) {
				t.Fatalf("result text = %q, want response path %q", text, tt.path)
			}
			if strings.Contains(text, tt.secretValue) {
				t.Fatalf("tool result leaked secret %q: %q", tt.secretValue, text)
			}
			if got := server.HitCount(); got != 1 {
				t.Fatalf("upstream hits = %d, want 1", got)
			}
			req := server.LastRequest(t)
			if req.Path != tt.path {
				t.Fatalf("upstream path = %q, want %q", req.Path, tt.path)
			}
			tt.assertUpstream(t, req)
		})
	}
}

func TestMCPServerRunsWasip2PackageHTTPClientDeniedAndMissingProxyRequests(t *testing.T) {
	tooltest.EnsureSandboxBinary(t)
	requireTSWasip2Artifacts(t)

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
			secrets:       map[string]string{tooltest.HTTPClientSecretKey(t, "query_key"): "query-secret-token"},
			wantErrSubstr: `transport denied request to host "127.0.0.2": not allowed by policy`,
			forbidSecret:  "query-secret-token",
		},
		{
			name:          "missing secret store surfaces explicit MCP error",
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
			restore := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
				proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
			})
			defer restore()

			store := testutil.NewTestSecretStore()
			store.SeedStrings(tt.secrets)
			resolved := tooltest.ResolveHTTPClientToolset(t, store)
			h := mcptest.NewHarness(t, mcpserver.New(resolved))

			result := h.CallTool(tooltest.HTTPClientToolName, map[string]any{"url": tt.url})
			if !result.IsError {
				t.Fatalf("expected MCP error result, got %#v", result.Content)
			}
			text := requireSingleTextContent(t, result)
			if !strings.Contains(text, tt.wantErrSubstr) {
				t.Fatalf("error text = %q, want %q", text, tt.wantErrSubstr)
			}
			if tt.forbidSecret != "" && strings.Contains(text, tt.forbidSecret) {
				t.Fatalf("error text leaked secret %q: %q", tt.forbidSecret, text)
			}
			if got := server.HitCount(); got != 0 {
				t.Fatalf("upstream hits = %d, want 0", got)
			}
		})
	}
}

func requireSingleTextContent(t testing.TB, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(result.Content))
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	return text.Text
}
