package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/audit"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestAuthHarnessGoogleEmulateGoFetch(t *testing.T) {
	harness := tooltest.NewGoogleAuthHarness(t)
	harness.UseRuntimeTLSRoots(t)
	const clientID = "client-google"

	var resourceHits atomic.Int32
	var lastAuthorization atomic.Value
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resourceHits.Add(1)
		lastAuthorization.Store(r.Header.Get("Authorization"))
		if got := r.URL.Path; got != "/admin/directory/v1/users" {
			t.Fatalf("request path = %q, want /admin/directory/v1/users", got)
		}
		if got := r.URL.Query().Get("customer"); got != "my_customer" {
			t.Fatalf("customer query = %q, want my_customer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[{"primaryEmail":"oauth-ada@example.com"}]}`))
	}))
	defer resourceServer.Close()
	toolboxDir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, harness.Provider)

	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{toolboxDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, newGoogleAuthHarnessDeps(harness))
	if err != nil {
		t.Fatalf("runAuthWithDeps() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `authorized oauth2 credential "workspace"`) {
		t.Fatalf("stdout = %q, want auth success output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "package scope") {
		t.Fatalf("stdout = %q, want package scope output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "public-client PKCE flow") {
		t.Fatalf("stdout = %q, want PKCE/public-client output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "OAuth client_id") || !strings.Contains(stderr.String(), "OAuth client_secret") {
		t.Fatalf("stderr = %q, want prompt text", stderr.String())
	}
	state := harness.AssertDurableOAuthState(t, "", clientID, "")
	if state.Namespace != tooltest.GoogleWorkspaceOAuthSecretFamily(t) {
		t.Fatalf("secret namespace = %q, want %q", state.Namespace, tooltest.GoogleWorkspaceOAuthSecretFamily(t))
	}

	collector := audit.NewCollector()
	resolved, err := tooltest.GoogleWorkspaceBuilderFromDir(t, toolboxDir).Resolve(toolset.Config{
		SecretStore: harness.Store,
		AuditSink:   collector,
	})
	if err != nil {
		t.Fatalf("Resolve(toolset.Config): %v", err)
	}
	tooltest.AssertGoogleWorkspaceAgentViewHidden(t, resolved)

	resultText, err := invoke.Run(resolved, "users.list", map[string]any{})
	if err != nil {
		t.Fatalf("invoke.Run(users.list): %v", err)
	}
	if strings.TrimSpace(resultText) == "" || resultText != "oauth-ada@example.com" {
		t.Fatalf("tool result = %q, want oauth-ada@example.com", resultText)
	}
	if got := resourceHits.Load(); got != 1 {
		t.Fatalf("protected resource hits = %d, want 1", got)
	}
	gotAuth, _ := lastAuthorization.Load().(string)
	if !strings.HasPrefix(gotAuth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(gotAuth, "Bearer ")) == "" {
		t.Fatalf("Authorization header = %q, want injected bearer token", gotAuth)
	}

	for _, forbidden := range []string{clientID, state.RefreshToken, strings.TrimSpace(strings.TrimPrefix(gotAuth, "Bearer ")), "access_token", "refresh_token", "client_secret"} {
		if strings.Contains(resultText, forbidden) {
			t.Fatalf("tool result leaked credential material %q: %q", forbidden, resultText)
		}
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("stdout leaked credential material %q: %q", forbidden, stdout.String())
		}
		if strings.Contains(stderr.String(), forbidden) && forbidden != "client_secret" {
			t.Fatalf("stderr leaked credential material %q: %q", forbidden, stderr.String())
		}
	}

	gotEvents := collector.Events()
	if len(gotEvents) != 2 {
		t.Fatalf("collector event count = %d, want 2", len(gotEvents))
	}
	refresh, ok := gotEvents[0].Payload.(audit.CredentialRefresh)
	if gotEvents[0].Name != audit.EventCredentialRefresh || !ok {
		t.Fatalf("event[0] = %#v, want credential_refresh", gotEvents[0])
	}
	if refresh.Outcome != "success" || refresh.Stage != "token_refresh" {
		t.Fatalf("refresh event = %#v, want success/token_refresh", refresh)
	}
	if refresh.Credential != "workspace" {
		t.Fatalf("refresh credential = %q, want workspace", refresh.Credential)
	}
	if refresh.CacheKey != "github.com/example/google-workspace:workspace" {
		t.Fatalf("refresh cache key = %q, want google-workspace cache key", refresh.CacheKey)
	}
	if refresh.Reason != "" || refresh.ExpiresAt.IsZero() {
		t.Fatalf("refresh event = %#v, want redaction-safe success payload", refresh)
	}

	injected, ok := gotEvents[1].Payload.(audit.CredentialInjected)
	if gotEvents[1].Name != audit.EventCredentialInjected || !ok {
		t.Fatalf("event[1] = %#v, want credential_injected", gotEvents[1])
	}
	resourceURL, err := url.Parse(resourceServer.URL)
	if err != nil {
		t.Fatalf("Parse(auth base url): %v", err)
	}
	if injected.Host != resourceURL.Hostname() {
		t.Fatalf("injected host = %q, want %q", injected.Host, resourceURL.Hostname())
	}
	if injected.Credential != "workspace" || injected.InjectMethod != "bearer_header" {
		t.Fatalf("injected event = %#v, want workspace/bearer_header", injected)
	}
	for i, event := range gotEvents {
		eventText := fmt.Sprintf("%#v", event)
		for _, forbidden := range []string{clientID, state.RefreshToken, "Bearer ", "google_access_"} {
			if strings.Contains(eventText, forbidden) {
				t.Fatalf("event[%d] leaked credential material %q: %s", i, forbidden, eventText)
			}
		}
	}
}

func TestAuthHarnessGoogleEmulateProxy(t *testing.T) {
	const clientID = "client-google"

	t.Run("browser auth feeds mitm-backed tswasm execution", func(t *testing.T) {
		harness := tooltest.NewGoogleAuthHarness(t)
		harness.UseRuntimeTLSRoots(t)
		tooltest.EnsureSandboxBinary(t)
		requireHTTPClientWasip2Artifacts(t)

		server := tooltest.StartHTTPClientLocalTLSServer(t)
		toolboxDir := tooltest.PrepareOAuthHTTPClientFixture(t, server.URL("/oauth/proxy"), harness.Provider)

		var stdout, stderr bytes.Buffer
		err := runAuthWithDeps([]string{toolboxDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, newGoogleAuthHarnessDeps(harness))
		if err != nil {
			t.Fatalf("runAuthWithDeps() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), `authorized oauth2 credential "workspace"`) {
			t.Fatalf("stdout = %q, want auth success output", stdout.String())
		}
		persistedKeys := tooltest.AssertHTTPClientDurableOAuthState(t, harness.Store, clientID, "")

		var proxiedHits atomic.Int32
		var proxyObservedURL atomic.Value
		var proxyObservedAuth atomic.Value
		restoreProxy := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
			proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
			proxy.Observer = mitmproxy.ObserverFunc(func(_ string, req *http.Request, _ *http.Response) {
				proxiedHits.Add(1)
				proxyObservedURL.Store(req.URL.String())
				proxyObservedAuth.Store(req.Header.Get("Authorization"))
			})
		})
		defer restoreProxy()

		collector := audit.NewCollector()
		resolved, err := tooltest.HTTPClientBuilderFromDir(t, toolboxDir).Resolve(toolset.Config{
			SecretStore: harness.Store,
			AuditSink:   collector,
		})
		if err != nil {
			t.Fatalf("Resolve(toolset.Config): %v", err)
		}
		tooltest.AssertHTTPClientAgentViewHidden(t, resolved)

		resultText, err := invoke.Run(resolved, tooltest.HTTPClientToolName, map[string]any{"url": server.URL("/oauth/proxy?via=mitm")})
		if err != nil {
			skipIfTinyGoWasip2HTTPUnavailable(t, err.Error())
			t.Fatalf("invoke.Run(%s): %v", tooltest.HTTPClientToolName, err)
		}
		if !strings.Contains(resultText, "Status: 200 OK") || !strings.Contains(resultText, `"path":"/oauth/proxy"`) {
			t.Fatalf("tool result = %q, want proxy-backed upstream response", resultText)
		}
		if got := server.HitCount(); got != 1 {
			t.Fatalf("upstream hits = %d, want 1", got)
		}
		if got := proxiedHits.Load(); got != 1 {
			t.Fatalf("proxy observer hits = %d, want 1", got)
		}
		proxyURL, _ := proxyObservedURL.Load().(string)
		if !strings.Contains(proxyURL, "/oauth/proxy?via=mitm") {
			t.Fatalf("proxy observed url = %q, want /oauth/proxy?via=mitm", proxyURL)
		}
		proxyAuth, _ := proxyObservedAuth.Load().(string)
		if !strings.HasPrefix(proxyAuth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(proxyAuth, "Bearer ")) == "" {
			t.Fatalf("proxy observed authorization = %q, want injected bearer token", proxyAuth)
		}
		upstreamReq := server.LastRequest(t)
		if got := upstreamReq.Path; got != "/oauth/proxy" {
			t.Fatalf("upstream path = %q, want /oauth/proxy", got)
		}
		if got := upstreamReq.Query; got != "via=mitm" {
			t.Fatalf("upstream query = %q, want via=mitm", got)
		}
		if got := upstreamReq.Header.Get("Authorization"); got != proxyAuth {
			t.Fatalf("upstream authorization = %q, want proxy-observed auth %q", got, proxyAuth)
		}

		refreshToken, err := harness.Store.Get(context.Background(), tooltest.HTTPClientOAuthSecretKey(t, "refresh_token"))
		if err != nil {
			t.Fatalf("Get(refresh_token): %v", err)
		}
		for _, forbidden := range []string{clientID, string(refreshToken), strings.TrimSpace(strings.TrimPrefix(proxyAuth, "Bearer ")), "access_token", "refresh_token", "client_secret"} {
			if strings.Contains(resultText, forbidden) {
				t.Fatalf("tool result leaked credential material %q: %q", forbidden, resultText)
			}
			if strings.Contains(stdout.String(), forbidden) {
				t.Fatalf("stdout leaked credential material %q: %q", forbidden, stdout.String())
			}
			if strings.Contains(stderr.String(), forbidden) && forbidden != "client_secret" {
				t.Fatalf("stderr leaked credential material %q: %q", forbidden, stderr.String())
			}
		}

		gotEvents := collector.Events()
		if len(gotEvents) != 2 {
			t.Fatalf("collector event count = %d, want 2", len(gotEvents))
		}
		refresh, ok := gotEvents[0].Payload.(audit.CredentialRefresh)
		if gotEvents[0].Name != audit.EventCredentialRefresh || !ok {
			t.Fatalf("event[0] = %#v, want credential_refresh", gotEvents[0])
		}
		if refresh.Outcome != "success" || refresh.Stage != "token_refresh" || refresh.Credential != tooltest.HTTPClientOAuthCredentialName {
			t.Fatalf("refresh event = %#v, want success/token_refresh/workspace", refresh)
		}
		if refresh.CacheKey != "github.com/example/http-client:workspace" || refresh.Reason != "" || refresh.ExpiresAt.IsZero() {
			t.Fatalf("refresh event = %#v, want redaction-safe http-client cache details", refresh)
		}
		injected, ok := gotEvents[1].Payload.(audit.CredentialInjected)
		if gotEvents[1].Name != audit.EventCredentialInjected || !ok {
			t.Fatalf("event[1] = %#v, want credential_injected", gotEvents[1])
		}
		if injected.Host != "127.0.0.1" || injected.Credential != tooltest.HTTPClientOAuthCredentialName || injected.InjectMethod != "bearer_header" {
			t.Fatalf("injected event = %#v, want 127.0.0.1/workspace/bearer_header", injected)
		}
		for i, event := range gotEvents {
			eventText := fmt.Sprintf("%#v", event)
			for _, forbidden := range append(persistedKeys, clientID, string(refreshToken), "Bearer ") {
				if strings.Contains(eventText, forbidden) {
					t.Fatalf("event[%d] leaked credential material %q: %s", i, forbidden, eventText)
				}
			}
		}
	})

	t.Run("host mismatch stays auditable and never reaches upstream", func(t *testing.T) {
		harness := tooltest.NewGoogleAuthHarness(t)
		harness.UseRuntimeTLSRoots(t)
		tooltest.EnsureSandboxBinary(t)
		requireHTTPClientWasip2Artifacts(t)

		server := tooltest.StartHTTPClientLocalTLSServer(t)
		toolboxDir := tooltest.PrepareOAuthHTTPClientFixture(t, "https://example.invalid/blocked", harness.Provider)
		var stdout, stderr bytes.Buffer
		if err := runAuthWithDeps([]string{toolboxDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, newGoogleAuthHarnessDeps(harness)); err != nil {
			t.Fatalf("runAuthWithDeps() error: %v", err)
		}

		restoreProxy := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
			proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
		})
		defer restoreProxy()

		collector := audit.NewCollector()
		resolved, err := tooltest.HTTPClientBuilderFromDir(t, toolboxDir).Resolve(toolset.Config{
			SecretStore: harness.Store,
			AuditSink:   collector,
		})
		if err != nil {
			t.Fatalf("Resolve(toolset.Config): %v", err)
		}

		_, err = invoke.Run(resolved, tooltest.HTTPClientToolName, map[string]any{"url": server.URL("/blocked")})
		if err == nil {
			t.Fatal("expected host allowlist denial")
		}
		skipIfTinyGoWasip2HTTPUnavailable(t, err.Error())
		if !strings.Contains(err.Error(), `transport denied request to host "127.0.0.1": not allowed by policy`) {
			t.Fatalf("error = %v, want host allowlist denial", err)
		}
		for _, forbidden := range []string{"google_refresh_", "access_token", "refresh_token", "client_secret", clientID, "Authorization:"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error leaked credential material %q: %v", forbidden, err)
			}
		}
		if got := server.HitCount(); got != 0 {
			t.Fatalf("upstream hits = %d, want 0", got)
		}
		gotEvents := collector.Events()
		if len(gotEvents) != 2 {
			t.Fatalf("collector event count = %d, want 2", len(gotEvents))
		}
		refresh, ok := gotEvents[0].Payload.(audit.CredentialRefresh)
		if gotEvents[0].Name != audit.EventCredentialRefresh || !ok || refresh.Outcome != "success" {
			t.Fatalf("event[0] = %#v, want successful credential_refresh before denial", gotEvents[0])
		}
		denied, ok := gotEvents[1].Payload.(audit.CredentialDenied)
		if gotEvents[1].Name != audit.EventCredentialDenied || !ok {
			t.Fatalf("event[1] = %#v, want credential_denied", gotEvents[1])
		}
		if denied.Host != "127.0.0.1" || denied.Reason != "not_allowed_by_policy" || denied.Credential != tooltest.HTTPClientOAuthCredentialName {
			t.Fatalf("denied event = %#v, want 127.0.0.1/not_allowed_by_policy/workspace", denied)
		}
	})

	t.Run("missing durable refresh state fails redacted before upstream access", func(t *testing.T) {
		harness := tooltest.NewGoogleAuthHarness(t)
		harness.UseRuntimeTLSRoots(t)
		tooltest.EnsureSandboxBinary(t)
		requireHTTPClientWasip2Artifacts(t)

		server := tooltest.StartHTTPClientLocalTLSServer(t)
		toolboxDir := tooltest.PrepareOAuthHTTPClientFixture(t, server.URL("/missing-refresh"), harness.Provider)
		var stdout, stderr bytes.Buffer
		if err := runAuthWithDeps([]string{toolboxDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, newGoogleAuthHarnessDeps(harness)); err != nil {
			t.Fatalf("runAuthWithDeps() error: %v", err)
		}
		if err := harness.Store.Delete(context.Background(), tooltest.HTTPClientOAuthSecretKey(t, "refresh_token")); err != nil {
			t.Fatalf("Delete(refresh_token): %v", err)
		}

		restoreProxy := invoke.SetMITMProxyConfiguratorForTest(func(proxy *mitmproxy.Proxy) {
			proxy.UpstreamTLSConfig = &tls.Config{RootCAs: server.RootCAs(t)}
		})
		defer restoreProxy()

		collector := audit.NewCollector()
		resolved, err := tooltest.HTTPClientBuilderFromDir(t, toolboxDir).Resolve(toolset.Config{
			SecretStore: harness.Store,
			AuditSink:   collector,
		})
		if err != nil {
			t.Fatalf("Resolve(toolset.Config): %v", err)
		}

		_, err = invoke.Run(resolved, tooltest.HTTPClientToolName, map[string]any{"url": server.URL("/missing-refresh")})
		if err == nil {
			t.Fatal("expected missing durable refresh failure")
		}
		skipIfTinyGoWasip2HTTPUnavailable(t, err.Error())
		if !strings.Contains(err.Error(), "failed during secret reread") || !strings.Contains(err.Error(), "re-authorize by updating client_id, client_secret, and refresh_token secrets") {
			t.Fatalf("error = %v, want durable refresh guidance", err)
		}
		for _, forbidden := range []string{"google_refresh_", "access_token", "refresh_token=", "client_secret", clientID, "Authorization:"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error leaked credential material %q: %v", forbidden, err)
			}
		}
		if got := server.HitCount(); got != 0 {
			t.Fatalf("upstream hits = %d, want 0", got)
		}
		gotEvents := collector.Events()
		if len(gotEvents) != 1 {
			t.Fatalf("collector event count = %d, want 1", len(gotEvents))
		}
		refresh, ok := gotEvents[0].Payload.(audit.CredentialRefresh)
		if gotEvents[0].Name != audit.EventCredentialRefresh || !ok {
			t.Fatalf("event[0] = %#v, want credential_refresh failure", gotEvents[0])
		}
		if refresh.Outcome != "failure" || refresh.Stage != "secret_reread" || refresh.Reason != "missing_secret_material" {
			t.Fatalf("refresh failure = %#v, want failure/secret_reread/missing_secret_material", refresh)
		}
	})
}

func newGoogleAuthHarnessDeps(harness *tooltest.GoogleAuthHarness) authDeps {
	return authDeps{
		prompt:   promptSecret,
		newStore: func() (secrets.SecretStore, error) { return harness.Store, nil },
		openBrowser: func(ctx context.Context, authURL string) error {
			return harness.Server.CompleteGoogleAuthorization(ctx, authURL)
		},
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			bootstrapper := oauthbootstrap.New(oauthbootstrap.Options{
				Store:           store,
				HTTPClient:      harness.Server.SecureClient(),
				OpenBrowser:     opener,
				CallbackTimeout: 5 * time.Second,
				ExchangeTimeout: 5 * time.Second,
			})
			return bootstrapper.Run
		},
	}
}

func requireHTTPClientWasip2Artifacts(t testing.TB) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(tooltest.HTTPClientFixtureDir(t), "dist", "http-client.wasm"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("wasip2 artifacts not ready: missing %s — rebuild with: cargo build --manifest-path wasmcli-sandbox/Cargo.toml", path)
		}
	}
}

func skipIfTinyGoWasip2HTTPUnavailable(t testing.TB, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, "Netdev not set") {
			t.Skip("skipping TinyGo wasip2 HTTP integration until wasmcli-sandbox exposes a compatible network device layer")
		}
	}
}
