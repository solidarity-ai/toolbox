package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/solidarity-ai/toolbox/audit"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestFinalIntegratedAcceptanceR076GoFetch(t *testing.T) {
	harness := tooltest.NewGoogleAuthHarness(t)
	harness.UseRuntimeTLSRoots(t)
	const clientID = "client-google"

	var protectedResourceHits atomic.Int32
	var lastAuthorization atomic.Value
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedResourceHits.Add(1)
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

	fixtureDir := tooltest.PrepareGoogleWorkspaceFixture(t, resourceServer.URL, harness.Provider)

	t.Run("author declares oauth package metadata without credential-shaped tool inputs", func(t *testing.T) {
		tooltest.AssertPreparedGoogleWorkspaceFixtureContract(t, fixtureDir, resourceServer.URL, harness.Provider)
	})

	var stdout, stderr bytes.Buffer
	t.Run("operator authorizes the package and persists durable refresh state", func(t *testing.T) {
		err := runAuthWithDeps([]string{fixtureDir}, strings.NewReader(clientID+"\n\n"), &stdout, &stderr, newGoogleAuthHarnessDeps(harness))
		if err != nil {
			t.Fatalf("runAuthWithDeps() error: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), `authorized oauth2 credential "workspace"`) {
			t.Fatalf("stdout = %q, want auth success output", stdout.String())
		}
		if !strings.Contains(stdout.String(), `module "github.com/example/google-workspace"`) {
			t.Fatalf("stdout = %q, want module identity", stdout.String())
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
	})

	t.Run("end user invokes goFetch successfully with redacted audit evidence and no credential inputs", func(t *testing.T) {
		collector := audit.NewCollector()
		resolved, err := tooltest.GoogleWorkspaceBuilderFromDir(t, fixtureDir).Resolve(toolset.Config{
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
		if resultText != "oauth-ada@example.com" {
			t.Fatalf("tool result = %q, want oauth-ada@example.com", resultText)
		}
		if got := protectedResourceHits.Load(); got != 1 {
			t.Fatalf("protected resource hits = %d, want 1", got)
		}
		gotAuth, _ := lastAuthorization.Load().(string)
		if !strings.HasPrefix(gotAuth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(gotAuth, "Bearer ")) == "" {
			t.Fatalf("Authorization header = %q, want injected bearer token", gotAuth)
		}

		state := harness.AssertDurableOAuthState(t, "", clientID, "")
		forbidden := []string{clientID, state.RefreshToken, strings.TrimSpace(strings.TrimPrefix(gotAuth, "Bearer ")), "access_token", "refresh_token", "client_secret"}
		assertAcceptanceOutputRedacted(t, resultText, stdout.String(), stderr.String(), forbidden)

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
			t.Fatalf("Parse(resourceServer.URL): %v", err)
		}
		if injected.Host != resourceURL.Hostname() {
			t.Fatalf("injected host = %q, want %q", injected.Host, resourceURL.Hostname())
		}
		if injected.Credential != "workspace" || injected.InjectMethod != "bearer_header" {
			t.Fatalf("injected event = %#v, want workspace/bearer_header", injected)
		}

		for i, event := range gotEvents {
			eventText := fmt.Sprintf("%#v", event)
			for _, secret := range []string{clientID, state.RefreshToken, "Bearer ", "google_access_"} {
				if strings.Contains(eventText, secret) {
					t.Fatalf("event[%d] leaked credential material %q: %s", i, secret, eventText)
				}
			}
		}
	})
}

func assertAcceptanceOutputRedacted(t testing.TB, resultText, stdoutText, stderrText string, forbidden []string) {
	t.Helper()
	for _, secret := range forbidden {
		if strings.Contains(resultText, secret) {
			t.Fatalf("tool result leaked credential material %q: %q", secret, resultText)
		}
		if strings.Contains(stdoutText, secret) {
			t.Fatalf("stdout leaked credential material %q: %q", secret, stdoutText)
		}
		if secret != "client_secret" && strings.Contains(stderrText, secret) {
			t.Fatalf("stderr leaked credential material %q: %q", secret, stderrText)
		}
	}
}
