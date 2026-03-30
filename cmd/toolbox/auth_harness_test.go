package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	deps := authDeps{
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

	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{toolboxDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, deps)
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
