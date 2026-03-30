package invoke_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestGoFetchAuthEmulateRouteReturns401WithoutInjectedAuth(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-unauth")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())
	rewriteGithubIssuesCredentialHost(t, workDir, "example.invalid")

	resolved := resolveGithubIssuesToolset(t, workDir, nil)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	assertUnauthenticatedAuthGateError(t, err, wantTitle)
}

func TestGoFetchAuthEmulateRouteReturns401WhenHostDoesNotMatchCredentialRule(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-host-miss")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())
	rewriteGithubIssuesCredentialHost(t, workDir, "example.invalid")

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": srv.Token(),
	})

	resolved := resolveGithubIssuesToolset(t, workDir, secretStore)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	assertUnauthenticatedAuthGateError(t, err, wantTitle)
}

func TestGoFetchAuthMissingTransportCredentialReturnsHelpfulError(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, _ := seedGithubIssueCanary(t, srv, "go-fetch-missing-secret")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	resolved := resolveGithubIssuesToolset(t, workDir, nil)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	if err == nil {
		t.Fatal("expected missing transport secret error")
	}
	if !strings.Contains(err.Error(), "no secret store") {
		t.Fatalf("error = %v, want missing secret store context", err)
	}
	if !strings.Contains(err.Error(), "github.com/example/github-issues/github_token") {
		t.Fatalf("error = %v, want package-scoped credential key", err)
	}
}

func TestGoFetchAuthEmulateRouteSucceedsWithResolvedTransportAuth(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-auth")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": srv.Token(),
	})

	resolved := resolveGithubIssuesToolset(t, workDir, secretStore)
	result, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var issue struct {
		Number int      `json:"number"`
		Title  string   `json:"title"`
		State  string   `json:"state"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(result), &issue); err != nil {
		t.Fatalf("unmarshal result: %v; raw=%s", err, result)
	}
	if issue.Number != issueNumber {
		t.Fatalf("issue number = %d, want %d", issue.Number, issueNumber)
	}
	if issue.Title != wantTitle {
		t.Fatalf("issue title = %q, want %q", issue.Title, wantTitle)
	}
	if issue.State != "open" {
		t.Fatalf("issue state = %q, want open", issue.State)
	}
	if strings.Contains(result, srv.Token()) {
		t.Fatalf("tool result leaked transport-managed auth token: %s", result)
	}
}

func TestGoFetchAuthGoogleWorkspaceFixtureTransportManagedOAuth(t *testing.T) {
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
		if got := r.Form.Get("client_secret"); got != "" {
			t.Fatalf("client_secret = %q, want empty for public client", got)
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
		if got := r.URL.Path; got != "/admin/directory/v1/users" {
			t.Fatalf("request path = %q, want /admin/directory/v1/users", got)
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
		_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"oauth-ada@example.com"}]}`))
	}))
	defer resourceServer.Close()

	secretStore := testutil.NewTestSecretStore()
	seedGoogleWorkspaceOAuthFixtureSecrets(t, secretStore, map[string]string{
		"client_id":     "client-google",
		"refresh_token": "refresh-google",
	})

	dir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider)
	resolved := resolveGoogleWorkspaceToolset(t, dir, secretStore)
	assertGoogleWorkspaceInvokeAgentViewHidden(t, resolved)

	result, err := invoke.Run(resolved, "users.list", map[string]any{})
	if err != nil {
		t.Fatalf("Run(users.list): %v", err)
	}
	if result != "oauth-ada@example.com" {
		t.Fatalf("tool result = %q, want oauth-ada@example.com", result)
	}
	for _, forbidden := range []string{"google-access-token", "refresh-google", "client-google"} {
		if strings.Contains(result, forbidden) {
			t.Fatalf("tool result leaked credential material %q: %s", forbidden, result)
		}
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", got)
	}
	if got := resourceHits.Load(); got != 1 {
		t.Fatalf("protected resource hits = %d, want 1", got)
	}

	policy, ok := resolved.ToolTransportPolicy("users.list")
	if !ok || policy == nil {
		t.Fatal("users.list transport policy missing")
	}
	if diff := strings.Join(policy.AllowedHosts(), ","); diff != "127.0.0.1" {
		t.Fatalf("allowed hosts = %v, want [127.0.0.1]", policy.AllowedHosts())
	}
	rules := policy.Rules()
	if len(rules) != 1 {
		t.Fatalf("transport rule count = %d, want 1", len(rules))
	}
	if got := rules[0].OAuth2SecretFamily; got != tooltest.GoogleWorkspaceOAuthSecretFamily(t) {
		t.Fatalf("oauth2 secret family = %q, want %q", got, tooltest.GoogleWorkspaceOAuthSecretFamily(t))
	}
}

func TestGoFetchAuthGoogleWorkspaceFixtureFailsClosed(t *testing.T) {
	t.Run("missing durable secret family aborts before token refresh or upstream fetch", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"should-not-be-used","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"never@example.com"}]}`))
		}))
		defer resourceServer.Close()

		resolved := resolveGoogleWorkspaceToolset(t, tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider), testutil.NewTestSecretStore())
		_, err := invoke.Run(resolved, "users.list", map[string]any{})
		if err == nil {
			t.Fatal("expected missing durable secret family error")
		}
		if !strings.Contains(err.Error(), "failed during secret reread") {
			t.Fatalf("error = %v, want secret reread context", err)
		}
		if !strings.Contains(err.Error(), "re-authorize by updating client_id, client_secret, and refresh_token secrets") {
			t.Fatalf("error = %v, want durable-secret remediation", err)
		}
		if got := tokenCalls.Load(); got != 0 {
			t.Fatalf("token endpoint calls = %d, want 0", got)
		}
		if got := resourceHits.Load(); got != 0 {
			t.Fatalf("protected resource hits = %d, want 0", got)
		}
	})

	t.Run("refresh failures stay redacted and never hit the protected resource", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"access_token":"leak-me","refresh_token":"leak-refresh"}`, http.StatusBadGateway)
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"never@example.com"}]}`))
		}))
		defer resourceServer.Close()

		secretStore := testutil.NewTestSecretStore()
		seedGoogleWorkspaceOAuthFixtureSecrets(t, secretStore, map[string]string{
			"client_id":     "client-google",
			"refresh_token": "refresh-google",
		})

		resolved := resolveGoogleWorkspaceToolset(t, tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider), secretStore)
		_, err := invoke.Run(resolved, "users.list", map[string]any{})
		if err == nil {
			t.Fatal("expected token refresh failure")
		}
		if !strings.Contains(err.Error(), "provider returned status 502") {
			t.Fatalf("error = %v, want token request failure", err)
		}
		for _, forbidden := range []string{"leak-me", "leak-refresh", "Authorization:"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error leaked refresh material %q: %v", forbidden, err)
			}
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
		if got := resourceHits.Load(); got != 0 {
			t.Fatalf("protected resource hits = %d, want 0", got)
		}
	})

	t.Run("wrong host rewrite is denied before any refresh or upstream traffic", func(t *testing.T) {
		provider, tokenServer, tokenCalls := newGoogleWorkspaceOAuthProviderHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"should-not-be-used","expires_in":3600}`))
		})
		defer tokenServer.Close()

		resourceHits := &atomic.Int32{}
		resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resourceHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"never@example.com"}]}`))
		}))
		defer resourceServer.Close()

		secretStore := testutil.NewTestSecretStore()
		seedGoogleWorkspaceOAuthFixtureSecrets(t, secretStore, map[string]string{
			"client_id":     "client-google",
			"refresh_token": "refresh-google",
		})

		dir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, provider)
		rewriteGoogleWorkspaceToolHostForTest(t, dir, "localhost")

		resolved := resolveGoogleWorkspaceToolset(t, dir, secretStore)
		_, err := invoke.Run(resolved, "users.list", map[string]any{})
		if err == nil {
			t.Fatal("expected wrong-host policy denial")
		}
		if !strings.Contains(err.Error(), `transport denied request to host "localhost": not allowed by policy`) {
			t.Fatalf("error = %v, want allowlist denial", err)
		}
		if got := tokenCalls.Load(); got != 0 {
			t.Fatalf("token endpoint calls = %d, want 0", got)
		}
		if got := resourceHits.Load(); got != 0 {
			t.Fatalf("protected resource hits = %d, want 0", got)
		}
	})
}

func TestGoFetchAuthMultiProviderOverrideAndNoAuthRuntimeSelection(t *testing.T) {
	t.Parallel()

	var protectedMu sync.Mutex
	protectedRequests := make([]runtimeAuthObservation, 0, 2)
	publicRequests := make([]runtimeAuthObservation, 0, 1)

	protectedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedMu.Lock()
		protectedRequests = append(protectedRequests, runtimeAuthObservation{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			PackageKey:    r.Header.Get("X-Package-Key"),
		})
		protectedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"surface":"protected"}`))
	}))
	defer protectedServer.Close()

	publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedMu.Lock()
		publicRequests = append(publicRequests, runtimeAuthObservation{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			PackageKey:    r.Header.Get("X-Package-Key"),
		})
		protectedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"surface":"public"}`))
	}))
	defer publicServer.Close()

	protectedHost := mustRuntimeTestHost(t, protectedServer.URL)
	publicHost := mustRuntimeTestHost(t, publicServer.URL)

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/mixed-auth/package_token":  "package-bearer",
		"github.com/example/mixed-auth/override_token": "override-bearer",
	})

	resolved := resolveMixedRuntimeAuthToolset(t, secretStore, protectedHost, publicHost)

	inheritResult, err := invoke.Run(resolved, "mixed.inherit", map[string]any{"url": protectedServer.URL + "/inherit"})
	if err != nil {
		t.Fatalf("Run(mixed.inherit): %v", err)
	}
	assertRuntimeToolResult(t, inheritResult, "protected", "package-bearer", "override-bearer")

	overrideResult, err := invoke.Run(resolved, "mixed.override", map[string]any{"url": protectedServer.URL + "/override"})
	if err != nil {
		t.Fatalf("Run(mixed.override): %v", err)
	}
	assertRuntimeToolResult(t, overrideResult, "protected", "package-bearer", "override-bearer")

	noneResult, err := invoke.Run(resolved, "mixed.none", map[string]any{"url": publicServer.URL + "/public"})
	if err != nil {
		t.Fatalf("Run(mixed.none): %v", err)
	}
	assertRuntimeToolResult(t, noneResult, "public", "package-bearer", "override-bearer")

	protectedMu.Lock()
	defer protectedMu.Unlock()
	if len(protectedRequests) != 2 {
		t.Fatalf("protected request count = %d, want 2", len(protectedRequests))
	}
	if protectedRequests[0].Path != "/inherit" {
		t.Fatalf("inherit path = %q, want /inherit", protectedRequests[0].Path)
	}
	if protectedRequests[0].Authorization != "Bearer package-bearer" {
		t.Fatalf("inherit authorization = %q, want Bearer package-bearer", protectedRequests[0].Authorization)
	}
	if protectedRequests[0].PackageKey != "" {
		t.Fatalf("inherit X-Package-Key = %q, want empty", protectedRequests[0].PackageKey)
	}
	if protectedRequests[1].Path != "/override" {
		t.Fatalf("override path = %q, want /override", protectedRequests[1].Path)
	}
	if protectedRequests[1].Authorization != "Bearer override-bearer" {
		t.Fatalf("override authorization = %q, want Bearer override-bearer", protectedRequests[1].Authorization)
	}
	if protectedRequests[1].PackageKey != "" {
		t.Fatalf("override X-Package-Key = %q, want empty to prove replace-not-merge", protectedRequests[1].PackageKey)
	}
	if len(publicRequests) != 1 {
		t.Fatalf("public request count = %d, want 1", len(publicRequests))
	}
	if publicRequests[0].Path != "/public" {
		t.Fatalf("public path = %q, want /public", publicRequests[0].Path)
	}
	if publicRequests[0].Authorization != "" {
		t.Fatalf("public authorization = %q, want empty", publicRequests[0].Authorization)
	}
	if publicRequests[0].PackageKey != "" {
		t.Fatalf("public X-Package-Key = %q, want empty", publicRequests[0].PackageKey)
	}
}

type runtimeAuthObservation struct {
	Path          string
	Authorization string
	PackageKey    string
}

func resolveMixedRuntimeAuthToolset(t testing.TB, secretStore secrets.SecretStore, protectedHost, publicHost string) toolset.ResolvedToolset {
	t.Helper()

	packageRoot := t.TempDir()
	entryRelPath := "tools/runtime-auth.fetch.ts"
	if err := os.MkdirAll(packageRoot+"/tools", 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	toolSource := `export default async function tool(args: { url: string }, _ctx: Record<string, unknown>): Promise<{status:number, body:string}> {
  const resp = await fetch(args.url, { headers: { Accept: "application/json" } });
  const body = await resp.text();
  if (resp.status !== 200) {
    throw new Error(` + "`" + `upstream ${resp.status}: ${body}` + "`" + `);
  }
  return { status: resp.status, body };
}
`
	if err := os.WriteFile(packageRoot+"/"+entryRelPath, []byte(toolSource), 0o644); err != nil {
		t.Fatalf("write runtime auth tool: %v", err)
	}

	basePkg := &tooldef.Package{
		Module:  "github.com/example/mixed-auth",
		Name:    "mixed-auth",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name: "package_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{protectedHost},
				Method: "bearer_header",
			},
		}},
	}

	toolFS := os.DirFS(packageRoot)
	tools := []tooldef.ResolvedTool{
		{
			Name:                 "mixed.inherit",
			Description:          "Uses inherited package credentials",
			Package:              basePkg,
			AllowedHosts:         []string{protectedHost},
			EffectiveCredentials: basePkg.Credentials,
			TS:                   &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
		{
			Name:         "mixed.override",
			Description:  "Uses replacement bearer credential",
			Package:      basePkg,
			AllowedHosts: []string{protectedHost},
			EffectiveCredentials: []tooldef.PackageCredential{{
				Name: "override_token",
				Type: tooldef.CredentialTypeBearer,
				Inject: tooldef.CredentialInject{
					Hosts:  []string{protectedHost},
					Method: "bearer_header",
				},
			}},
			TS: &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
		{
			Name:         "mixed.none",
			Description:  "Explicit no-auth public tool",
			Package:      basePkg,
			AllowedHosts: []string{publicHost},
			TS:           &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
	}

	for i := range tools {
		tools[i].SetParamsSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
			"required": []any{"url"},
		})
	}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}

func assertRuntimeToolResult(t testing.TB, raw, wantSurface string, forbiddenSubstrings ...string) {
	t.Helper()
	for _, forbidden := range forbiddenSubstrings {
		if forbidden != "" && strings.Contains(raw, forbidden) {
			t.Fatalf("tool result leaked runtime credential material %q: %s", forbidden, raw)
		}
	}
	var payload struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal runtime tool result: %v; raw=%s", err, raw)
	}
	if payload.Status != http.StatusOK {
		t.Fatalf("tool status = %d, want %d", payload.Status, http.StatusOK)
	}
	if !strings.Contains(payload.Body, `"surface":"`+wantSurface+`"`) {
		t.Fatalf("tool body = %q, want surface %q", payload.Body, wantSurface)
	}
}

func mustRuntimeTestHost(t testing.TB, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	return parsed.Hostname()
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

func resolveGoogleWorkspaceToolset(t testing.TB, workDir string, secretStore secrets.SecretStore) toolset.ResolvedToolset {
	t.Helper()

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}
	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}

func assertGoogleWorkspaceInvokeAgentViewHidden(t testing.TB, resolved toolset.ResolvedToolset) {
	t.Helper()
	view := resolved.AgentView()
	if len(view.Tools) != 1 {
		t.Fatalf("agent view tool count = %d, want 1", len(view.Tools))
	}
	if view.Tools[0].Name != "users.list" {
		t.Fatalf("agent view tool name = %q, want users.list", view.Tools[0].Name)
	}
	propsAny, hasProps := view.Tools[0].ParamsSchema["properties"]
	if !hasProps || propsAny == nil {
		return
	}
	props, ok := propsAny.(map[string]any)
	if !ok {
		t.Fatalf("params schema properties malformed: %#v", view.Tools[0].ParamsSchema)
	}
	if len(props) != 0 {
		t.Fatalf("users.list params schema properties = %#v, want none", props)
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "workspace"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("credential-shaped param %q should not appear in AgentView schema", forbidden)
		}
	}
}

func rewriteGoogleWorkspaceToolHostForTest(t testing.TB, dir, host string) {
	t.Helper()
	toolPath := dir + "/tools/users.list.ts"
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

func resolveGithubIssuesToolset(t testing.TB, workDir string, secretStore secrets.SecretStore) toolset.ResolvedToolset {
	t.Helper()

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}

	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}

func seedGithubIssueCanary(t testing.TB, srv *emulatetest.Server, prefix string) (owner, repo string, issueNumber int, title string) {
	t.Helper()

	seed := srv.Seed()
	repo = fmt.Sprintf("%s-%s", prefix, strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
	owner = "admin"

	createdRepo, err := seed.CreateRepo(owner, repo)
	if err != nil && !strings.Contains(err.Error(), "Repository already exists") {
		t.Fatalf("CreateRepo(%s/%s): %v", owner, repo, err)
	}
	if createdRepo != nil && createdRepo.FullName != "" {
		parts := strings.SplitN(createdRepo.FullName, "/", 2)
		if len(parts) == 2 {
			owner = parts[0]
			repo = parts[1]
		}
	}

	title = fmt.Sprintf("auth canary %s", prefix)
	issue, err := seed.CreateIssue(owner, repo, title)
	if err != nil {
		t.Fatalf("CreateIssue(%s/%s): %v", owner, repo, err)
	}
	return owner, repo, issue.Number, title
}

func rewriteGithubIssuesCredentialHost(t testing.TB, workDir, host string) {
	t.Helper()

	manifestPath, manifest := loadGithubIssuesManifestForRewrite(t, workDir)
	credentials, ok := manifest["credentials"].([]any)
	if !ok || len(credentials) != 1 {
		t.Fatalf("credentials malformed in %s: %#v", manifestPath, manifest["credentials"])
	}
	credential, ok := credentials[0].(map[string]any)
	if !ok {
		t.Fatalf("credential malformed in %s: %#v", manifestPath, credentials[0])
	}
	inject, ok := credential["inject"].(map[string]any)
	if !ok {
		t.Fatalf("inject config malformed in %s: %#v", manifestPath, credential["inject"])
	}
	inject["hosts"] = []string{host}
	writeGithubIssuesManifestRewrite(t, manifestPath, manifest)
}

func loadGithubIssuesManifestForRewrite(t testing.TB, workDir string) (string, map[string]any) {
	t.Helper()

	manifestPath := workDir + "/toolbox.devpkg.json"
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest for host rewrite: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest for host rewrite: %v", err)
	}
	return manifestPath, manifest
}

func writeGithubIssuesManifestRewrite(t testing.TB, manifestPath string, manifest map[string]any) {
	t.Helper()

	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest for host rewrite: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write manifest for host rewrite: %v", err)
	}
}

func assertUnauthenticatedAuthGateError(t testing.TB, err error, wantTitle string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected auth-gated emulate route to reject unauthenticated fetch")
	}
	if !strings.Contains(err.Error(), "GitHub API 401") {
		t.Fatalf("error = %v, want GitHub API 401", err)
	}
	if !strings.Contains(err.Error(), "Authorization: Bearer <redacted>") {
		t.Fatalf("error = %v, want auth-gate diagnostic", err)
	}
	if strings.Contains(err.Error(), wantTitle) {
		t.Fatalf("error leaked upstream issue payload instead of failing at auth gate: %v", err)
	}
}
