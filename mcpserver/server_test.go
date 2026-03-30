package mcpserver_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestMCPServerListsVisibleInvokeTools(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
	assertContains(t, names, "calc.asyncAdd")
}

func TestMCPServerExposesResolvedParamSchema(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))
	tools := h.ListTools()

	var calcAdd *mcp.Tool
	for i := range tools.Tools {
		if tools.Tools[i].Name == "calc.add" {
			calcAdd = &tools.Tools[i]
			break
		}
	}
	if calcAdd == nil {
		t.Fatal("expected calc.add in MCP tool list")
	}

	if calcAdd.InputSchema.Type != "object" {
		t.Fatalf("input schema type = %q, want object", calcAdd.InputSchema.Type)
	}
	if len(calcAdd.InputSchema.Properties) == 0 {
		t.Fatalf("input schema properties = %#v, want non-empty schema", calcAdd.InputSchema.Properties)
	}

	aSchema, ok := calcAdd.InputSchema.Properties["a"].(map[string]any)
	if !ok {
		t.Fatalf("property a = %#v, want schema object", calcAdd.InputSchema.Properties["a"])
	}
	if aSchema["type"] != "number" {
		t.Fatalf("property a type = %#v, want number", aSchema["type"])
	}

	bSchema, ok := calcAdd.InputSchema.Properties["b"].(map[string]any)
	if !ok {
		t.Fatalf("property b = %#v, want schema object", calcAdd.InputSchema.Properties["b"])
	}
	if bSchema["type"] != "number" {
		t.Fatalf("property b type = %#v, want number", bSchema["type"])
	}
}

func TestMCPServerCallsInvokeForTool(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": 5,
		"b": 5,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "10" {
		t.Fatalf("expected result 10, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForDifferentArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.asyncAdd", map[string]any{
		"a": 7,
		"b": 4,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "11" {
		t.Fatalf("expected result 11, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForStringAndNumberArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": "6",
		"b": 3,
	})
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected error content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if !strings.Contains(text.Text, "typescript check failed") {
		t.Fatalf("expected typecheck failure, got %#v", text.Text)
	}
}

func TestMCPServerCallsInvokeForDistArchivePackage(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcDistToolset(t)))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
	assertContains(t, names, "calc.sub")
	assertContains(t, names, "calc.asyncAdd")

	result := h.CallTool("calc.add", map[string]any{
		"a": 3,
		"b": 7,
	})
	if result.IsError {
		if len(result.Content) > 0 {
			text, _ := mcp.AsTextContent(result.Content[0])
			t.Fatalf("expected non-error result, got: %s", text.Text)
		}
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "10" {
		t.Fatalf("expected result 10, got %#v", text.Text)
	}
}

func TestMCPServerRunsGoogleWorkspaceFixtureFromDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/directory/v1/users" {
			t.Fatalf("request path = %q, want /admin/directory/v1/users", r.URL.Path)
		}
		if got := r.URL.Query().Get("customer"); got != "my_customer" {
			t.Fatalf("customer query = %q, want my_customer", got)
		}
		if got := r.URL.Query().Get("maxResults"); got != "1" {
			t.Fatalf("maxResults query = %q, want 1", got)
		}
		if got := r.URL.Query().Get("orderBy"); got != "email" {
			t.Fatalf("orderBy query = %q, want email", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"fetch-ada@example.com"}]}`))
	}))
	defer server.Close()

	dir := rewriteGoogleWorkspaceSourceFixtureForTest(t, server.URL)
	h := googleWorkspaceHarness(t, dir)
	assertGoogleWorkspaceSchemaHasNoCredentialInputs(t, h.ListTools())
	assertGoogleWorkspaceFixtureResult(t, h.CallTool("users.list", map[string]any{}), "fetch-ada@example.com")
}

func TestMCPServerRunsGoogleWorkspaceFixtureFromCopiedDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"copy-ada@example.com"}]}`))
	}))
	defer server.Close()

	dir := prepareGoogleWorkspaceFixtureWithoutCredentials(t, server.URL)
	h := googleWorkspaceHarness(t, dir)
	assertGoogleWorkspaceSchemaHasNoCredentialInputs(t, h.ListTools())
	assertGoogleWorkspaceFixtureResult(t, h.CallTool("users.list", map[string]any{}), "copy-ada@example.com")
}

func TestMCPServerRunsGoogleWorkspaceFixtureWithTransportManagedOAuth(t *testing.T) {
	t.Run("happy path keeps oauth transport-owned and schema-clean", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth/token" {
				t.Fatalf("token path = %q, want /oauth/token", r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", got)
			}
			if got := r.Form.Get("client_id"); got != "client-google" {
				t.Fatalf("client_id = %q, want client-google", got)
			}
			if got := r.Form.Get("refresh_token"); got != "refresh-google" {
				t.Fatalf("refresh_token = %q, want refresh-google", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"google-access-token","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer google-access-token" {
				t.Fatalf("Authorization header = %q, want Bearer google-access-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"mcp-ada@example.com"}]}`))
		}))
		defer resourceServer.Close()

		store := testutil.NewTestSecretStore()
		seedGoogleWorkspaceOAuthFixtureSecrets(t, store, map[string]string{
			"client_id":     "client-google",
			"refresh_token": "refresh-google",
		})

		dir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider)
		h := googleWorkspaceHarnessWithSecretStore(t, dir, store)
		assertGoogleWorkspaceSchemaHasNoCredentialInputs(t, h.ListTools())
		assertGoogleWorkspaceFixtureResult(t, h.CallTool("users.list", map[string]any{}), "mcp-ada@example.com")
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
		if got := resourceHits.Load(); got != 1 {
			t.Fatalf("protected resource hits = %d, want 1", got)
		}
	})

	t.Run("missing durable secret family fails before refresh or upstream fetch", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"should-not-be-used","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"never@example.com"}]}`))
		}))
		defer resourceServer.Close()

		h := googleWorkspaceHarnessWithSecretStore(t, tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider), testutil.NewTestSecretStore())
		result := h.CallTool("users.list", map[string]any{})
		assertGoogleWorkspaceFixtureError(t, result, "failed during secret reread")
		assertGoogleWorkspaceFixtureError(t, result, "re-authorize by updating client_id, client_secret, and refresh_token secrets")
		if got := tokenCalls.Load(); got != 0 {
			t.Fatalf("token endpoint calls = %d, want 0", got)
		}
		if got := resourceHits.Load(); got != 0 {
			t.Fatalf("protected resource hits = %d, want 0", got)
		}
	})

	t.Run("malformed protected-resource payload stays redacted after auth injection", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"google-access-token","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer google-access-token" {
				t.Fatalf("Authorization header = %q, want Bearer google-access-token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"users":`))
		}))
		defer resourceServer.Close()

		store := testutil.NewTestSecretStore()
		seedGoogleWorkspaceOAuthFixtureSecrets(t, store, map[string]string{
			"client_id":     "client-google",
			"refresh_token": "refresh-google",
		})

		h := googleWorkspaceHarnessWithSecretStore(t, tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider), store)
		result := h.CallTool("users.list", map[string]any{})
		assertGoogleWorkspaceFixtureError(t, result, "google workspace response was not valid JSON")
		text := requireSingleTextContent(t, result)
		for _, forbidden := range []string{"google-access-token", "refresh-google", "Authorization:"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("tool error leaked auth material %q: %s", forbidden, text)
			}
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
		if got := resourceHits.Load(); got != 1 {
			t.Fatalf("protected resource hits = %d, want 1", got)
		}
	})

	t.Run("denied host fails before auth refresh", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"should-not-be-used","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"never@example.com"}]}`))
		}))
		defer resourceServer.Close()

		store := testutil.NewTestSecretStore()
		seedGoogleWorkspaceOAuthFixtureSecrets(t, store, map[string]string{
			"client_id":     "client-google",
			"refresh_token": "refresh-google",
		})

		dir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider)
		rewriteGoogleWorkspaceToolHostForTest(t, dir, "localhost")
		result := googleWorkspaceHarnessWithSecretStore(t, dir, store).CallTool("users.list", map[string]any{})
		assertGoogleWorkspaceFixtureError(t, result, `transport denied request to host "localhost": not allowed by policy`)
		if got := tokenCalls.Load(); got != 0 {
			t.Fatalf("token endpoint calls = %d, want 0", got)
		}
		if got := resourceHits.Load(); got != 0 {
			t.Fatalf("protected resource hits = %d, want 0", got)
		}
	})
}

func TestMCPServerRunsGoogleWorkspaceFixtureRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":`))
	}))
	defer server.Close()

	dir := prepareGoogleWorkspaceFixtureWithoutCredentials(t, server.URL)
	h := googleWorkspaceHarness(t, dir)
	assertGoogleWorkspaceFixtureError(t, h.CallTool("users.list", map[string]any{}), "google workspace response was not valid JSON")
}

func TestMCPServerRunsGoogleWorkspaceFixtureRejectsMissingPrimaryEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"name":"Ada"}]}`))
	}))
	defer server.Close()

	dir := prepareGoogleWorkspaceFixtureWithoutCredentials(t, server.URL)
	h := googleWorkspaceHarness(t, dir)
	assertGoogleWorkspaceFixtureError(t, h.CallTool("users.list", map[string]any{}), "google workspace response missing users[0].primaryEmail")
}

func TestMCPServerRunsGoogleWorkspaceFixtureRejectsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`Authorization: Bearer should-not-leak`))
	}))
	defer server.Close()

	dir := prepareGoogleWorkspaceFixtureWithoutCredentials(t, server.URL)
	h := googleWorkspaceHarness(t, dir)
	result := h.CallTool("users.list", map[string]any{})
	assertGoogleWorkspaceFixtureError(t, result, "Google Workspace API 502")
	text := requireSingleTextContent(t, result)
	if strings.Contains(text, "should-not-leak") || strings.Contains(text, "Authorization:") {
		t.Fatalf("tool error leaked upstream auth material: %s", text)
	}
}

func rewriteGoogleWorkspaceSourceFixtureForTest(t *testing.T, baseURL string) string {
	t.Helper()
	return prepareGoogleWorkspaceFixtureWithoutCredentials(t, baseURL)
}

func prepareGoogleWorkspaceFixtureWithoutCredentials(t testing.TB, baseURL string) string {
	t.Helper()
	dir := tooltest.PrepareGoogleWorkspaceFixture(t, baseURL, tooldef.OAuth2ProviderRef{})
	manifestPath := filepath.Join(dir, "toolbox.devpkg.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read google-workspace manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("decode google-workspace manifest: %v", err)
	}
	delete(manifest, "credentials")
	updatedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode google-workspace manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updatedManifest, '\n'), 0o644); err != nil {
		t.Fatalf("write google-workspace manifest: %v", err)
	}
	return dir
}

func googleWorkspaceHarness(t testing.TB, dir string) *mcptest.Harness {
	t.Helper()
	builder := tooltest.GoogleWorkspaceBuilderFromDir(t, dir)
	return mcptest.NewHarness(t, mcpserver.New(mustResolveWithConfig(t, builder, toolset.Config{})))
}

func googleWorkspaceHarnessWithSecretStore(t testing.TB, dir string, store *testutil.TestSecretStore) *mcptest.Harness {
	t.Helper()
	builder := tooltest.GoogleWorkspaceBuilderFromDir(t, dir)
	return mcptest.NewHarness(t, mcpserver.New(mustResolveWithConfig(t, builder, toolset.Config{SecretStore: store})))
}

func newGoogleWorkspaceOAuthProviderHarness(t testing.TB, tokenHandler http.HandlerFunc) (tooldef.OAuth2ProviderRef, *httptest.Server, *atomic.Int32) {
	t.Helper()

	tokenCalls := &atomic.Int32{}
	tokenServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		tokenHandler(w, r)
	}))
	restore := trustOAuthProviderServerForDefaultTransport(t, tokenServer)
	t.Cleanup(restore)

	return tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
		AuthURL:  tokenServer.URL + "/oauth/authorize",
		TokenURL: tokenServer.URL + "/oauth/token",
	}}, tokenServer, tokenCalls
}

func trustOAuthProviderServerForDefaultTransport(t testing.TB, server *httptest.Server) func() {
	t.Helper()

	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport = %T, want *http.Transport", http.DefaultTransport)
	}
	clone := baseTransport.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	if clone.TLSClientConfig == nil {
		clone.TLSClientConfig = &tls.Config{}
	} else {
		clone.TLSClientConfig = clone.TLSClientConfig.Clone()
	}
	clone.TLSClientConfig.RootCAs = pool

	previous := http.DefaultTransport
	http.DefaultTransport = clone
	return func() {
		http.DefaultTransport = previous
	}
}

func seedGoogleWorkspaceOAuthFixtureSecrets(t testing.TB, store *testutil.TestSecretStore, members map[string]string) {
	t.Helper()
	seed := make(map[string]string, len(members))
	for member, value := range members {
		seed[tooltest.GoogleWorkspaceOAuthSecretKey(t, member)] = value
	}
	store.SeedStrings(seed)
}

func rewriteGoogleWorkspaceToolHostForTest(t testing.TB, dir, host string) {
	t.Helper()
	toolPath := filepath.Join(dir, "tools", "users.list.ts")
	raw, err := os.ReadFile(toolPath)
	if err != nil {
		t.Fatalf("read google-workspace tool for host rewrite: %v", err)
	}
	updated := strings.Replace(string(raw), "127.0.0.1", host, 1)
	if updated == string(raw) {
		t.Fatalf("google-workspace tool host rewrite found no 127.0.0.1 host in %s", toolPath)
	}
	if err := os.WriteFile(toolPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write google-workspace tool for host rewrite: %v", err)
	}
}

func assertGoogleWorkspaceSchemaHasNoCredentialInputs(t testing.TB, tools *mcp.ListToolsResult) {
	t.Helper()

	var usersList *mcp.Tool
	for i := range tools.Tools {
		if tools.Tools[i].Name == "users.list" {
			usersList = &tools.Tools[i]
			break
		}
	}
	if usersList == nil {
		t.Fatal("expected users.list in MCP tool list")
	}
	if usersList.InputSchema.Type != "object" {
		t.Fatalf("users.list input schema type = %q, want object", usersList.InputSchema.Type)
	}
	if len(usersList.InputSchema.Properties) != 0 {
		t.Fatalf("users.list input schema properties = %#v, want no tool-visible inputs", usersList.InputSchema.Properties)
	}
}

func assertGoogleWorkspaceFixtureResult(t testing.TB, result *mcp.CallToolResult, want string) {
	t.Helper()
	if result.IsError {
		text := requireSingleTextContent(t, result)
		t.Fatalf("expected non-error result, got: %s", text)
	}
	text := requireSingleTextContent(t, result)
	if text != want {
		t.Fatalf("tool result = %q, want %q", text, want)
	}
}

func assertGoogleWorkspaceFixtureError(t testing.TB, result *mcp.CallToolResult, wantSubstring string) {
	t.Helper()
	if !result.IsError {
		text := requireSingleTextContent(t, result)
		t.Fatalf("expected error result, got: %s", text)
	}
	text := requireSingleTextContent(t, result)
	if !strings.Contains(text, wantSubstring) {
		t.Fatalf("tool error = %q, want substring %q", text, wantSubstring)
	}
}

func TestMCPServerRunsWasip2PackageHTTPClient(t *testing.T) {
	requireTSWasip2Artifacts(t)
	tooltest.EnsureSandboxBinary(t)

	server := tooltest.StartHTTPClientLocalTLSServer(t)
	restore := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
		proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
	})
	defer restore()

	resolved := tooltest.ResolveHTTPClientToolset(t, testutil.NewTestSecretStore())
	h := mcptest.NewHarness(t, mcpserver.New(resolved))
	result := h.CallTool("httpClient.fetch", map[string]any{"url": server.URL("/plain/mcp")})
	if result.IsError {
		text := requireSingleTextContent(t, result)
		skipIfTinyGoWasip2HTTPUnavailable(t, text)
		t.Fatalf("expected non-error result")
	}

	resultStr := requireSingleTextContent(t, result)
	if !strings.Contains(resultStr, "Status: 200 OK") {
		t.Fatalf("expected result to contain 'Status: 200 OK', got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "/plain/mcp") {
		t.Fatalf("expected result to contain '/plain/mcp', got:\n%s", resultStr)
	}
	if got := server.HitCount(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

func requireTSWasip2Artifacts(t *testing.T) {
	t.Helper()

	tooltest.EnsureSandboxBinary(t)

	paths := []string{
		filepath.Join(httpClientFixtureDir(), "toolbox.devpkg.json"),
		filepath.Join(httpClientFixtureDir(), "dist", "http-client.wasm"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("wasip2 artifacts not ready: missing %s", p)
		}
	}
}

func httpClientFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("mcpserver_test: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "http-client")
}

func TestMCPServerFetchToolMakesHTTPRequest(t *testing.T) {
	// Start a test HTTP server that returns known content.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"greeting":"hello from test server"}`))
	}))
	t.Cleanup(srv.Close)

	h := mcptest.NewHarness(t, mcpserver.New(tooltest.FetchTestToolset(t)))

	// Invoke the fetch-test.get tool with the test server URL.
	result := h.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL,
	})
	if result.IsError {
		if len(result.Content) > 0 {
			text, _ := mcp.AsTextContent(result.Content[0])
			t.Fatalf("expected non-error result, got: %s", text.Text)
		}
		t.Fatalf("expected non-error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	resultStr := text.Text

	// The tool returns JSON with status and body.
	var fetchResult struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(resultStr), &fetchResult); err != nil {
		t.Fatalf("parse fetch result: %v (raw: %s)", err, resultStr)
	}
	if fetchResult.Status != 200 {
		t.Fatalf("expected status 200, got %d", fetchResult.Status)
	}
	if !strings.Contains(fetchResult.Body, "hello from test server") {
		t.Fatalf("expected body to contain greeting, got: %s", fetchResult.Body)
	}
}

func mustResolve(t testing.TB, builder *toolset.Builder) toolset.ResolvedToolset {
	t.Helper()
	return mustResolveWithConfig(t, builder, toolset.Config{})
}

func mustResolveWithConfig(t testing.TB, builder *toolset.Builder, cfg toolset.Config) toolset.ResolvedToolset {
	t.Helper()
	resolved, err := builder.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}
	return resolved
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
}
