package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

func mcpResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty result content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return tc.Text
}

func TestMCP_CredentialInjection_BearerToken(t *testing.T) {
	t.Parallel()

	gotAuth := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		gotAuth.Store(auth)
		if auth != "Bearer test-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"users":["alice"]}`))
	}))
	t.Cleanup(upstream.Close)

	tokenSrv := newTokenServer(t, "test-access-token")
	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "myapi", "default", "default", "test-client-id", "test-client-secret", "test-refresh-token")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newOAuth2Rule(upstreamURL.Hostname(), "myapi", "default", credpath.AccountPrefix("myapi", "default", "default"), tokenSrv.URL+"/token"),
		},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/users",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "users") {
		t.Fatalf("expected result to contain upstream API response with 'users', got: %s", text)
	}

	if strings.Contains(text, "test-access-token") {
		t.Fatalf("access token leaked to tool result: %s", text)
	}

	if gotAuth.Load() != "Bearer test-access-token" {
		t.Fatalf("upstream did not receive expected Authorization header, got: %q", gotAuth.Load())
	}
}

func TestMCP_CredentialInjection_NoMatchingHost(t *testing.T) {
	t.Parallel()

	gotAuth := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"public":true}`))
	}))
	t.Cleanup(upstream.Close)

	tokenSrv := newTokenServer(t, "should-not-be-used")

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "myapi", "default", "default", "test-client-id", "test-client-secret", "test-refresh-token")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newOAuth2Rule("no-match.example.com", "myapi", "default", credpath.AccountPrefix("myapi", "default", "default"), tokenSrv.URL+"/token"),
		},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "public") {
		t.Fatalf("expected result to contain upstream response, got: %s", text)
	}

	if gotAuth.Load() != "" {
		t.Fatalf("upstream received unexpected Authorization header: %q", gotAuth.Load())
	}
}

func TestMCP_HostAllowlist_BlocksRequest(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`should not reach here`))
	}))
	t.Cleanup(upstream.Close)

	al := transport.NewHostAllowlist([]string{"allowed.example.com"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/blocked",
	})

	errText := mcpResultErrorText(t, result)
	if !strings.Contains(errText, "not in allowlist") {
		t.Fatalf("expected 'not in allowlist' error, got: %s", errText)
	}
}

func TestMCP_CredentialInjection_CustomHeaderName(t *testing.T) {
	t.Parallel()

	gotHeader := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader.Store(r.Header.Get("Api-Key"))
		if xapi := r.Header.Get("X-API-Key"); xapi != "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"unexpected X-API-Key header"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"custom-header-ok"}`))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "weather", "default", "default", "WEATHER-KEY-789")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newAPIKeyHeaderRule(upstreamURL.Hostname(), "weather", "default", credpath.AccountPrefix("weather", "default", "default"), "Api-Key"),
		},
		store,
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/forecast",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "custom-header-ok") {
		t.Fatalf("expected upstream response, got: %s", text)
	}

	if gotHeader.Load() != "WEATHER-KEY-789" {
		t.Fatalf("upstream received Api-Key %q, want %q", gotHeader.Load(), "WEATHER-KEY-789")
	}
}

func TestMCP_StripInjected_CustomHeaderOnRedirect(t *testing.T) {
	t.Parallel()

	gotHeader := newHeaderCapture()
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader.Store(r.Header.Get("Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"redirected":true}`))
	}))
	t.Cleanup(destination.Close)

	destURL, _ := url.Parse(destination.URL)
	localhostDest := "http://localhost:" + destURL.Port()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localhostDest+"/landed", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "svc", "cred", "default", "SECRET-CUSTOM-KEY")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newAPIKeyHeaderRule("127.0.0.1", "svc", "cred", credpath.AccountPrefix("svc", "cred", "default"), "Api-Key"),
		},
		store,
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1", "localhost"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": origin.URL + "/start",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}

	if gotHeader.Load() != "" {
		t.Fatalf("Api-Key header leaked to redirect destination: %q", gotHeader.Load())
	}
}

func TestMCP_StripCallerSuppliedHeaders_OnSameHostHTTPSDowngrade(t *testing.T) {
	gotAuth := newHeaderCapture()
	gotAPIKey := newHeaderCapture()

	srv := newSameHostDualSchemeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.TLS != nil && r.URL.Path == "/start":
			http.Redirect(w, r, "http://"+r.Host+"/landed", http.StatusFound)
		case r.TLS == nil && r.URL.Path == "/landed":
			gotAuth.Store(r.Header.Get("Authorization"))
			gotAPIKey.Store(r.Header.Get("X-API-Key"))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"redirected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	trustCertInDefaultTransport(t, srv.certPEM)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{})

	result := harness.CallTool("fetchTest.withHeaders", map[string]any{
		"url":           srv.URL("https", "/start"),
		"authorization": "Bearer caller-secret",
		"xApiKey":       "caller-api-key",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}
	if gotAuth.Load() != "" {
		t.Fatalf("Authorization header leaked to downgrade destination: %q", gotAuth.Load())
	}
	if gotAPIKey.Load() != "" {
		t.Fatalf("X-API-Key header leaked to downgrade destination: %q", gotAPIKey.Load())
	}
}

func TestMCP_StripInjected_APIKeyQueryOnRedirect(t *testing.T) {
	t.Parallel()

	gotURL := newHeaderCapture()
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL.Store(r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"redirected":true}`))
	}))
	t.Cleanup(destination.Close)

	destParsed, _ := url.Parse(destination.URL)
	localhostDest := "http://localhost:" + destParsed.Port()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localhostDest+"/landed", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "maps", "default", "default", "MAPS-KEY-SECRET")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			{
				Hosts:                    []string{"127.0.0.1"},
				ModuleName:               "maps",
				CredentialName:           "default",
				SecretPrefix:             credpath.AccountPrefix("maps", "default", "default"),
				Type:                     transport.CredentialTypeAPIKey,
				Method:                   transport.InjectionMethodAPIKeyQuery,
				AllowUnsafeHTTPInjection: true,
			},
		},
		store,
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1", "localhost"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": origin.URL + "/geocode",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}

	landedURL := gotURL.Load()
	if strings.Contains(landedURL, "MAPS-KEY-SECRET") {
		t.Fatalf("API key query param leaked to redirect destination URL: %s", landedURL)
	}
	if strings.Contains(landedURL, "key=") {
		t.Fatalf("key= query param leaked to redirect destination URL: %s", landedURL)
	}
}

func TestMCP_StripInjected_APIKeyQueryOnSameHostHTTPSDowngrade(t *testing.T) {
	gotURL := newHeaderCapture()

	srv := newSameHostDualSchemeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.TLS != nil && r.URL.Path == "/geocode":
			location := "http://" + r.Host + "/landed"
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, location, http.StatusFound)
		case r.TLS == nil && r.URL.Path == "/landed":
			gotURL.Store(r.URL.String())
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"redirected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	trustCertInDefaultTransport(t, srv.certPEM)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "maps", "default", "default", "MAPS-KEY-SECRET")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			{
				Hosts:          []string{"127.0.0.1"},
				ModuleName:     "maps",
				CredentialName: "default",
				SecretPrefix:   credpath.AccountPrefix("maps", "default", "default"),
				Type:           transport.CredentialTypeAPIKey,
				Method:         transport.InjectionMethodAPIKeyQuery,
			},
		},
		store,
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL("https", "/geocode"),
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}

	landedURL := gotURL.Load()
	if strings.Contains(landedURL, "MAPS-KEY-SECRET") {
		t.Fatalf("API key query param leaked to downgrade destination URL: %s", landedURL)
	}
	if strings.Contains(landedURL, "key=") {
		t.Fatalf("key= query param leaked to downgrade destination URL: %s", landedURL)
	}
}

func TestMCP_StripInjected_BearerOnRedirect(t *testing.T) {
	t.Parallel()

	gotAuth := newHeaderCapture()
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"redirected":true}`))
	}))
	t.Cleanup(destination.Close)

	destURL, _ := url.Parse(destination.URL)
	localhostDest := "http://localhost:" + destURL.Port()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localhostDest+"/landed", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	tokenSrv := newTokenServer(t, "bearer-secret-token")

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "api", "cred", "default", "cid", "csec", "rt")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newOAuth2Rule("127.0.0.1", "api", "cred", credpath.AccountPrefix("api", "cred", "default"), tokenSrv.URL+"/token"),
		},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1", "localhost"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": origin.URL + "/start",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}

	if gotAuth.Load() != "" {
		t.Fatalf("Authorization header leaked to redirect destination: %q", gotAuth.Load())
	}
}

func TestMCP_StripInjected_BearerOnSameHostHTTPSDowngrade(t *testing.T) {
	gotAuth := newHeaderCapture()

	srv := newSameHostDualSchemeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.TLS != nil && r.URL.Path == "/start":
			http.Redirect(w, r, "http://"+r.Host+"/landed", http.StatusFound)
		case r.TLS == nil && r.URL.Path == "/landed":
			gotAuth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"redirected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	trustCertInDefaultTransport(t, srv.certPEM)

	tokenSrv := newTokenServer(t, "bearer-secret-token")

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "api", "cred", "default", "cid", "csec", "rt")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newOAuth2Rule("127.0.0.1", "api", "cred", credpath.AccountPrefix("api", "cred", "default"), tokenSrv.URL+"/token"),
		},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL("https", "/start"),
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}
	if gotAuth.Load() != "" {
		t.Fatalf("Authorization header leaked to downgrade destination: %q", gotAuth.Load())
	}
}

func TestMCP_StripInjected_BearerWhenRedirectEscapesPathPrefix(t *testing.T) {
	gotAuth := newHeaderCapture()

	srv := newSameHostDualSchemeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.TLS != nil && r.URL.Path == "/private/start":
			http.Redirect(w, r, "https://"+r.Host+"/public/landed", http.StatusFound)
		case r.TLS != nil && r.URL.Path == "/public/landed":
			gotAuth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"redirected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	trustCertInDefaultTransport(t, srv.certPEM)

	tokenSrv := newTokenServer(t, "path-secret-token")

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "api", "cred", "default", "cid", "csec", "rt")

	rule := newOAuth2Rule("127.0.0.1", "api", "cred", credpath.AccountPrefix("api", "cred", "default"), tokenSrv.URL+"/token")
	rule.PathPrefix = "/private/"

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{rule},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	al := transport.NewHostAllowlist([]string{"127.0.0.1"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci, Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL("https", "/private/start"),
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}
	if gotAuth.Load() != "" {
		t.Fatalf("Authorization header leaked outside path_prefix: %q", gotAuth.Load())
	}
}

func TestMCP_ReinjectsBearerWhenRedirectLandsOnDifferentMatchingRule(t *testing.T) {
	t.Parallel()

	gotPrivateAuth := newHeaderCapture()
	gotPartnerAuth := newHeaderCapture()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/private/start":
			gotPrivateAuth.Store(r.Header.Get("Authorization"))
			http.Redirect(w, r, "/partner/landed", http.StatusFound)
		case "/partner/landed":
			gotPartnerAuth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"redirected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	srvURL, _ := url.Parse(srv.URL)

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.BearerToken("private-api", "private", "default"): []byte("private-token"),
		credpath.BearerToken("partner-api", "partner", "default"): []byte("partner-token"),
	})

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			{
				Hosts:                    []string{srvURL.Hostname()},
				PathPrefix:               "/private/",
				ModuleName:               "private-api",
				CredentialName:           "private",
				SecretPrefix:             credpath.AccountPrefix("private-api", "private", "default"),
				Type:                     transport.CredentialTypeBearer,
				Method:                   transport.InjectionMethodBearerHeader,
				AllowUnsafeHTTPInjection: true,
			},
			{
				Hosts:                    []string{srvURL.Hostname()},
				PathPrefix:               "/partner/",
				ModuleName:               "partner-api",
				CredentialName:           "partner",
				SecretPrefix:             credpath.AccountPrefix("partner-api", "partner", "default"),
				Type:                     transport.CredentialTypeBearer,
				Method:                   transport.InjectionMethodBearerHeader,
				AllowUnsafeHTTPInjection: true,
			},
		},
		store,
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": srv.URL + "/private/start",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "redirected") {
		t.Fatalf("expected redirected response, got: %s", text)
	}
	if gotPrivateAuth.Load() != "Bearer private-token" {
		t.Fatalf("origin received Authorization %q, want %q", gotPrivateAuth.Load(), "Bearer private-token")
	}
	if gotPartnerAuth.Load() != "Bearer partner-token" {
		t.Fatalf("redirect destination received Authorization %q, want %q", gotPartnerAuth.Load(), "Bearer partner-token")
	}
}

func TestMCP_CredentialInjection_APIKey(t *testing.T) {
	t.Parallel()

	gotAPIKey := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		gotAPIKey.Store(key)
		if key != "secret-api-key-12345" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"sensitive-payload"}`))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "myservice", "prod", "default", "secret-api-key-12345")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newAPIKeyHeaderRule(upstreamURL.Hostname(), "myservice", "prod", credpath.AccountPrefix("myservice", "prod", "default"), ""),
		},
		store,
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/secret",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "sensitive-payload") {
		t.Fatalf("expected result to contain 'sensitive-payload', got: %s", text)
	}

	if strings.Contains(text, "secret-api-key-12345") {
		t.Fatalf("API key leaked to tool result: %s", text)
	}

	if gotAPIKey.Load() != "secret-api-key-12345" {
		t.Fatalf("upstream did not receive expected X-API-Key header, got: %q", gotAPIKey.Load())
	}
}

func TestMCP_CredentialInjection_HTTPRejectedByDefault(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"unexpected":true}`))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "myservice", "prod", "default", "secret-api-key-12345")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			{
				Hosts:          []string{upstreamURL.Hostname()},
				ModuleName:     "myservice",
				CredentialName: "prod",
				SecretPrefix:   credpath.AccountPrefix("myservice", "prod", "default"),
				Type:           transport.CredentialTypeAPIKey,
				Method:         transport.InjectionMethodAPIKeyHeader,
			},
		},
		store,
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/secret",
	})

	errText := mcpResultErrorText(t, result)
	if !strings.Contains(errText, "credential injection failed") {
		t.Fatalf("expected generic credential injection failure, got: %s", errText)
	}
	if requests.Load() != 0 {
		t.Fatalf("upstream received %d requests, want 0", requests.Load())
	}
}

func TestMCP_CredentialInjection_HTTPAllowedWithOptIn(t *testing.T) {
	t.Parallel()

	gotAPIKey := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey.Store(r.Header.Get("X-API-Key"))
		if gotAPIKey.Load() != "secret-api-key-12345" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"insecure-opt-in-ok"}`))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, "myservice", "prod", "default", "secret-api-key-12345")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			{
				Hosts:                    []string{upstreamURL.Hostname()},
				ModuleName:               "myservice",
				CredentialName:           "prod",
				SecretPrefix:             credpath.AccountPrefix("myservice", "prod", "default"),
				Type:                     transport.CredentialTypeAPIKey,
				Method:                   transport.InjectionMethodAPIKeyHeader,
				AllowUnsafeHTTPInjection: true,
			},
		},
		store,
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/secret",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "insecure-opt-in-ok") {
		t.Fatalf("expected insecure opt-in response, got: %s", text)
	}
	if gotAPIKey.Load() != "secret-api-key-12345" {
		t.Fatalf("upstream did not receive expected X-API-Key header, got: %q", gotAPIKey.Load())
	}
}

// mcpResultErrorText extracts the text from an error MCP tool result.
func mcpResultErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("expected error result, got success")
	}
	if len(result.Content) == 0 {
		t.Fatal("empty error content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return tc.Text
}

// findToolInputSchema returns the InputSchema.Properties for a named tool from ListTools.
func findToolInputSchema(t *testing.T, harness *mcptest.Harness, toolName string) map[string]any {
	t.Helper()
	tools := harness.ListTools()
	for _, tool := range tools.Tools {
		if tool.Name == toolName {
			return tool.InputSchema.Properties
		}
	}
	t.Fatalf("tool %q not found in ListTools result", toolName)
	return nil
}

// assertAccountParam checks that the tool schema has a {cred}_account property with the expected enum values.
func assertAccountParam(t *testing.T, harness *mcptest.Harness, toolName, paramName string, wantEnum []string) {
	t.Helper()
	props := findToolInputSchema(t, harness, toolName)
	raw, ok := props[paramName]
	if !ok {
		t.Fatalf("expected %q property in tool %q schema, but it was missing. Properties: %v", paramName, toolName, props)
	}
	schema, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("property %q is %T, want map[string]any", paramName, raw)
	}
	enumRaw, ok := schema["enum"]
	if !ok {
		t.Fatalf("property %q has no enum field. Schema: %v", paramName, schema)
	}
	enumSlice, ok := enumRaw.([]any)
	if !ok {
		t.Fatalf("property %q enum is %T, want []any", paramName, enumRaw)
	}
	if len(enumSlice) != len(wantEnum) {
		t.Fatalf("property %q enum length = %d, want %d. Got: %v", paramName, len(enumSlice), len(wantEnum), enumSlice)
	}
	for i, v := range wantEnum {
		got := fmt.Sprintf("%v", enumSlice[i])
		if got != v {
			t.Errorf("property %q enum[%d] = %q, want %q", paramName, i, got, v)
		}
	}
}

// assertNoAccountParam checks that the tool schema does NOT have a {cred}_account property.
func assertNoAccountParam(t *testing.T, harness *mcptest.Harness, toolName, paramName string) {
	t.Helper()
	props := findToolInputSchema(t, harness, toolName)
	if _, ok := props[paramName]; ok {
		t.Fatalf("expected NO %q property in tool %q schema, but it was present. Properties: %v", paramName, toolName, props)
	}
}

// loadAuthTestFixtureMCP loads the auth-test fixture package and builds injection rules.
func loadAuthTestFixtureMCP(t *testing.T) (packaging.LoadedPackage, []transport.InjectionRule) {
	t.Helper()
	loadedPkgs, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("auth-test"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pkg, ok := loadedPkgs.Package("auth-test")
	if !ok {
		t.Fatal("auth-test package not found")
	}
	loaded := pkg.Package
	rules := credentialrepo.BuildInjectionRules(loaded.Package)
	if len(rules) != 2 {
		t.Fatalf("expected 2 injection rules (test_api + test_oauth), got %d", len(rules))
	}
	return loaded, rules
}

// overrideOAuth2TokenURLMCP replaces the TokenURL in all OAuth2 rules with the given URL.
func overrideOAuth2TokenURLMCP(rules []transport.InjectionRule, tokenURL string) {
	for i := range rules {
		if rules[i].Type == transport.CredentialTypeOAuth2 && rules[i].Provider != nil {
			rules[i].Provider.TokenURL = tokenURL
		}
	}
}

// parseFetchResultMCP parses the JSON result from authTest.get tool.
func parseFetchResultMCP(t *testing.T, result string) (int, string) {
	t.Helper()
	var fr struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(result), &fr); err != nil {
		t.Fatalf("parse fetch result: %v (raw: %s)", err, result)
	}
	return fr.Status, fr.Body
}

// TestMCP_MultiAccount_SingleAccount_NoAccountParamNeeded verifies that when only
// one account exists for a credential (stored under /accounts/), no account param
// is generated and the credential is injected automatically via auto-selection.
func TestMCP_MultiAccount_SingleAccount_NoAccountParamNeeded(t *testing.T) {
	t.Parallel()

	loaded, rules := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	store := testutil.NewTestSecretStore()
	seedAPIKey(store, module, "test_api", "default", "KEY-PROD")

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	if len(credAccounts["test_api"]) != 1 || credAccounts["test_api"][0] != "default" {
		t.Fatalf("expected [default] for test_api, got %v", credAccounts["test_api"])
	}

	gotAPIKey := newHeaderCapture()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey.Store(r.Header.Get("X-API-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"hello"}`))
	}))
	t.Cleanup(upstream.Close)

	ci := transport.NewCredentialInjector(rules, store)
	al := transport.NewHostAllowlist(loaded.Package.AllowedHosts)
	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
		Injector:           ci,
		Allowlist:          al,
	})

	// Verify that no test_api_account param is exposed when only one account exists.
	assertNoAccountParam(t, harness, "authTest.get", "test_api_account")

	result := harness.CallTool("authTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	text := mcpResultText(t, result)

	if gotAPIKey.Load() != "KEY-PROD" {
		t.Errorf("upstream received X-API-Key %q, want %q", gotAPIKey.Load(), "KEY-PROD")
	}

	status, body := parseFetchResultMCP(t, text)
	if status != 200 {
		t.Errorf("expected status 200, got %d", status)
	}
	if !strings.Contains(body, "hello") {
		t.Errorf("expected upstream response, got: %s", body)
	}

	if strings.Contains(text, "KEY-PROD") {
		t.Errorf("API key leaked to tool result: %s", text)
	}
}

// TestMCP_MultiAccount_TwoAccounts_SameCredential verifies that when two accounts
// exist for the same credential, a {cred}_account param is required and the
// correct account's secret is used for injection.
func TestMCP_MultiAccount_TwoAccounts_SameCredential(t *testing.T) {
	t.Parallel()

	loaded, rules := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.APIKey(module, "test_api", "prod"):    []byte("KEY-PROD"),
		credpath.APIKey(module, "test_api", "staging"): []byte("KEY-STAGING"),
	})

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	accounts := credAccounts["test_api"]
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts for test_api, got %v", accounts)
	}
	if accounts[0] != "prod" || accounts[1] != "staging" {
		t.Fatalf("expected [prod, staging], got %v", accounts)
	}

	gotAPIKey := newHeaderCapture()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		gotAPIKey.Store(key)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"received_key_length": len(key)})
	}))
	t.Cleanup(upstream.Close)

	ci := transport.NewCredentialInjector(rules, store)
	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
		Injector:           ci,
	})

	// Verify that test_api_account enum param is exposed with both account names.
	assertAccountParam(t, harness, "authTest.get", "test_api_account", []string{"prod", "staging"})

	t.Run("select_prod", func(t *testing.T) {
		gotAPIKey.Store("")
		result := harness.CallTool("authTest.get", map[string]any{
			"url":              upstream.URL + "/api/data",
			"test_api_account": "prod",
		})
		text := mcpResultText(t, result)
		if gotAPIKey.Load() != "KEY-PROD" {
			t.Errorf("upstream received X-API-Key %q, want %q", gotAPIKey.Load(), "KEY-PROD")
		}
		if strings.Contains(text, "KEY-PROD") {
			t.Errorf("API key leaked to tool result: %s", text)
		}
	})

	t.Run("select_staging", func(t *testing.T) {
		gotAPIKey.Store("")
		result := harness.CallTool("authTest.get", map[string]any{
			"url":              upstream.URL + "/api/data",
			"test_api_account": "staging",
		})
		text := mcpResultText(t, result)
		if gotAPIKey.Load() != "KEY-STAGING" {
			t.Errorf("upstream received X-API-Key %q, want %q", gotAPIKey.Load(), "KEY-STAGING")
		}
		if strings.Contains(text, "KEY-STAGING") {
			t.Errorf("API key leaked to tool result: %s", text)
		}
	})

	t.Run("missing_account_param_fails", func(t *testing.T) {
		result := harness.CallTool("authTest.get", map[string]any{
			"url": upstream.URL + "/api/data",
		})
		errText := mcpResultErrorText(t, result)
		if !strings.Contains(errText, "credential injection failed") {
			t.Errorf("expected 'credential injection failed' error, got: %s", errText)
		}
	})
}

// TestMCP_MultiAccount_TwoCredentials_DifferentTypes exercises injection when
// two different credentials (api_key and oauth2) each have accounts.
func TestMCP_MultiAccount_TwoCredentials_DifferentTypes(t *testing.T) {
	t.Parallel()

	loaded, rules := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	tokenSrv := newTokenServerWithRefreshMap(t, map[string]string{
		"RT-WORK":     "ACCESS-WORK",
		"RT-PERSONAL": "ACCESS-PERSONAL",
	})

	overrideOAuth2TokenURLMCP(rules, tokenSrv.URL+"/refresh")

	// Phase 1: Single account per credential (auto-select).
	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.APIKey(module, "test_api", "prod"):               []byte("KEY-PROD"),
		credpath.OAuth2ClientID(module, "test_oauth"):             []byte("CID"),
		credpath.OAuth2ClientSecret(module, "test_oauth"):         []byte("CSEC"),
		credpath.OAuth2RefreshToken(module, "test_oauth", "work"): []byte("RT-WORK"),
	})

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	if len(credAccounts["test_api"]) != 1 {
		t.Fatalf("expected 1 account for test_api, got %v", credAccounts["test_api"])
	}
	if len(credAccounts["test_oauth"]) != 1 {
		t.Fatalf("expected 1 account for test_oauth, got %v", credAccounts["test_oauth"])
	}

	gotAPIKey := newHeaderCapture()
	gotAuth := newHeaderCapture()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey.Store(r.Header.Get("X-API-Key"))
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	oauthURL := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)

	ci := transport.NewCredentialInjector(rules, store, transport.WithRefreshClient(tokenSrv.Client()))
	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
		Injector:           ci,
	})

	// Phase 1: single account per credential — no account params needed (auto-select).
	assertNoAccountParam(t, harness, "authTest.get", "test_api_account")
	assertNoAccountParam(t, harness, "authTest.get", "test_oauth_account")

	t.Run("api_key_single_account", func(t *testing.T) {
		gotAPIKey.Store("")
		result := harness.CallTool("authTest.get", map[string]any{
			"url":              upstream.URL + "/api/data",
			"test_api_account": "prod",
		})
		text := mcpResultText(t, result)
		if gotAPIKey.Load() != "KEY-PROD" {
			t.Errorf("upstream got X-API-Key %q, want KEY-PROD", gotAPIKey.Load())
		}
		if strings.Contains(text, "KEY-PROD") {
			t.Errorf("key leaked: %s", text)
		}
	})

	t.Run("oauth2_single_account", func(t *testing.T) {
		gotAuth.Store("")
		result := harness.CallTool("authTest.get", map[string]any{
			"url":                oauthURL + "/secured/users",
			"test_oauth_account": "work",
		})
		text := mcpResultText(t, result)
		if gotAuth.Load() != "Bearer ACCESS-WORK" {
			t.Errorf("upstream got Authorization %q, want Bearer ACCESS-WORK", gotAuth.Load())
		}
		if strings.Contains(text, "ACCESS-WORK") {
			t.Errorf("token leaked: %s", text)
		}
	})

	// Phase 2: Add a second account per credential. Re-discover and re-prepare.
	store.Seed(map[string][]byte{
		credpath.APIKey(module, "test_api", "staging"):                []byte("KEY-STAGING"),
		credpath.OAuth2RefreshToken(module, "test_oauth", "personal"): []byte("RT-PERSONAL"),
	})

	credAccounts, err = credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts (phase 2): %v", err)
	}

	if len(credAccounts["test_api"]) != 2 {
		t.Fatalf("expected 2 accounts for test_api, got %v", credAccounts["test_api"])
	}
	if len(credAccounts["test_oauth"]) != 2 {
		t.Fatalf("expected 2 accounts for test_oauth, got %v", credAccounts["test_oauth"])
	}

	ci2 := transport.NewCredentialInjector(rules, store, transport.WithRefreshClient(tokenSrv.Client()))
	harness2 := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
		Injector:           ci2,
	})

	// Phase 2: two accounts per credential — both account params should have two-value enums.
	assertAccountParam(t, harness2, "authTest.get", "test_api_account", []string{"prod", "staging"})
	assertAccountParam(t, harness2, "authTest.get", "test_oauth_account", []string{"personal", "work"})

	t.Run("api_key_staging_account", func(t *testing.T) {
		gotAPIKey.Store("")
		result := harness2.CallTool("authTest.get", map[string]any{
			"url":              upstream.URL + "/api/data",
			"test_api_account": "staging",
		})
		text := mcpResultText(t, result)
		if gotAPIKey.Load() != "KEY-STAGING" {
			t.Errorf("upstream got X-API-Key %q, want KEY-STAGING", gotAPIKey.Load())
		}
		if strings.Contains(text, "KEY-STAGING") {
			t.Errorf("key leaked: %s", text)
		}
	})

	t.Run("oauth2_personal_account", func(t *testing.T) {
		gotAuth.Store("")
		result := harness2.CallTool("authTest.get", map[string]any{
			"url":                oauthURL + "/secured/users",
			"test_oauth_account": "personal",
		})
		text := mcpResultText(t, result)
		if gotAuth.Load() != "Bearer ACCESS-PERSONAL" {
			t.Errorf("upstream got Authorization %q, want Bearer ACCESS-PERSONAL", gotAuth.Load())
		}
		if strings.Contains(text, "ACCESS-PERSONAL") {
			t.Errorf("token leaked: %s", text)
		}
	})
}

// TestMCP_MultiAccount_MultiFetch_TwoCredentialsTwoHosts exercises the multi-fetch
// tool with two different credentials on two different hosts: an API key on
// 127.0.0.1 and an OAuth2 bearer token on localhost.
func TestMCP_MultiAccount_MultiFetch_TwoCredentialsTwoHosts(t *testing.T) {
	t.Parallel()

	loaded, rules := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	tokenSrv := newTokenServer(t, "OAUTH-BEARER-TOKEN")
	overrideOAuth2TokenURLMCP(rules, tokenSrv.URL+"/refresh")

	store := testutil.NewTestSecretStore()
	// This test uses fixture-generated rules which have SharedPrefix-based
	// SecretPrefix values. Seed secrets at the paths the rules will look up:
	// API key at SharedPrefix + "api_key", OAuth2 client creds at shared paths,
	// and refresh_token at SharedPrefix + "refresh_token".
	store.Seed(map[string][]byte{
		credpath.Shared(module, "test_api", "api_key"):         []byte("SECRET-API-KEY"),
		credpath.OAuth2ClientID(module, "test_oauth"):          []byte("test-client-id"),
		credpath.OAuth2ClientSecret(module, "test_oauth"):      []byte("test-client-secret"),
		credpath.Shared(module, "test_oauth", "refresh_token"): []byte("test-refresh-token"),
	})

	ci := transport.NewCredentialInjector(rules, store, transport.WithRefreshClient(tokenSrv.Client()))
	al := transport.NewHostAllowlist(loaded.Package.AllowedHosts)

	gotAPIKey := newHeaderCapture()
	gotAuth := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			gotAPIKey.Store(r.Header.Get("X-API-Key"))
		}
		if strings.HasPrefix(r.URL.Path, "/secured/") {
			gotAuth.Store(r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"path":"` + r.URL.Path + `"}`))
	}))
	t.Cleanup(upstream.Close)

	apiURL := upstream.URL
	oauthURL := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)

	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		Injector:  ci,
		Allowlist: al,
	})

	result := harness.CallTool("authTest.multiFetch", map[string]any{
		"url1": apiURL + "/api/data",
		"url2": oauthURL + "/secured/users",
	})
	text := mcpResultText(t, result)

	var mfResult struct {
		Response1 struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"response1"`
		Response2 struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"response2"`
	}
	if err := json.Unmarshal([]byte(text), &mfResult); err != nil {
		t.Fatalf("parse multi-fetch result: %v (raw: %s)", err, text)
	}

	if gotAPIKey.Load() != "SECRET-API-KEY" {
		t.Errorf("API upstream received X-API-Key %q, want %q", gotAPIKey.Load(), "SECRET-API-KEY")
	}

	if gotAuth.Load() != "Bearer OAUTH-BEARER-TOKEN" {
		t.Errorf("OAuth upstream received Authorization %q, want %q", gotAuth.Load(), "Bearer OAUTH-BEARER-TOKEN")
	}

	if strings.Contains(mfResult.Response1.Body, "SECRET-API-KEY") {
		t.Errorf("API key leaked to response1 body: %s", mfResult.Response1.Body)
	}
	if strings.Contains(mfResult.Response2.Body, "OAUTH-BEARER-TOKEN") {
		t.Errorf("OAuth token leaked to response2 body: %s", mfResult.Response2.Body)
	}
	if strings.Contains(text, "SECRET-API-KEY") {
		t.Errorf("API key leaked to tool result: %s", text)
	}
	if strings.Contains(text, "OAUTH-BEARER-TOKEN") {
		t.Errorf("OAuth token leaked to tool result: %s", text)
	}
}

// TestMCP_ToolsetInjector_UsedByDefault verifies that the MCP server uses the
// toolset-level CredentialInjector when it was set on the Config during Prepare.
func TestMCP_ToolsetInjector_UsedByDefault(t *testing.T) {
	t.Parallel()

	gotAuth := newHeaderCapture()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	tokenSrv := newTokenServer(t, "toolset-token")
	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	seedOAuth2(store, "myapi", "default", "default", "cid", "csec", "rtok")

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{
			newOAuth2Rule(upstreamURL.Hostname(), "myapi", "default", credpath.AccountPrefix("myapi", "default", "default"), tokenSrv.URL+"/token"),
		},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Injector: ci})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	text := mcpResultText(t, result)

	if !strings.Contains(text, "ok") {
		t.Fatalf("expected ok in result, got: %s", text)
	}

	if gotAuth.Load() != "Bearer toolset-token" {
		t.Fatalf("upstream got Authorization %q, want %q", gotAuth.Load(), "Bearer toolset-token")
	}
}

// TestMCP_ToolsetAllowlist_UsedByDefault verifies that the MCP server uses the
// toolset-level HostAllowlist when it was set on the Config during Prepare.
func TestMCP_ToolsetAllowlist_UsedByDefault(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`should not reach here`))
	}))
	t.Cleanup(upstream.Close)

	al := transport.NewHostAllowlist([]string{"allowed.example.com"})
	harness := newFetchTestHarness(t, toolset.PackageCredentialPolicy{Allowlist: al})

	result := harness.CallTool("fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/blocked",
	})
	errText := mcpResultErrorText(t, result)
	if !strings.Contains(errText, "not in allowlist") {
		t.Fatalf("expected 'not in allowlist' error, got: %s", errText)
	}
}

// TestMCP_NoCredentialAccounts_NoAccountParams verifies that when CredentialAccounts
// is nil/empty in Config, no _account params appear in any tool schema.
func TestMCP_NoCredentialAccounts_NoAccountParams(t *testing.T) {
	t.Parallel()

	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{})

	for _, toolName := range []string{"authTest.get", "authTest.multiFetch"} {
		assertNoAccountParam(t, harness, toolName, "test_api_account")
		assertNoAccountParam(t, harness, toolName, "test_oauth_account")
	}
}

// TestMCP_AccountParams_ApplyToAllTools verifies that when 2+ accounts exist
// for a credential, the {cred}_account param appears on EVERY tool in the toolset.
func TestMCP_AccountParams_ApplyToAllTools(t *testing.T) {
	t.Parallel()

	loaded, _ := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.APIKey(module, "test_api", "prod"):    []byte("KEY-PROD"),
		credpath.APIKey(module, "test_api", "staging"): []byte("KEY-STAGING"),
	})

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
	})

	for _, toolName := range []string{"authTest.get", "authTest.multiFetch"} {
		assertAccountParam(t, harness, toolName, "test_api_account", []string{"prod", "staging"})
	}
}

// TestMCP_AccountParams_PartialCardinality verifies that when one credential has
// 2+ accounts (gets an enum param) and another has only 1 (auto-selects, no param),
// the schema reflects this correctly — mixed cardinality within the same toolset.
func TestMCP_AccountParams_PartialCardinality(t *testing.T) {
	t.Parallel()

	loaded, _ := loadAuthTestFixtureMCP(t)
	module := loaded.Package.Module.String()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.APIKey(module, "test_api", "prod"):               []byte("KEY-PROD"),
		credpath.APIKey(module, "test_api", "staging"):            []byte("KEY-STAGING"),
		credpath.OAuth2ClientID(module, "test_oauth"):             []byte("CID"),
		credpath.OAuth2ClientSecret(module, "test_oauth"):         []byte("CSEC"),
		credpath.OAuth2RefreshToken(module, "test_oauth", "work"): []byte("RT-WORK"),
	})

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	harness := newAuthTestHarness(t, toolset.PackageCredentialPolicy{
		CredentialAccounts: credAccounts,
	})

	// test_api has 2 accounts → enum param should exist on all tools.
	for _, toolName := range []string{"authTest.get", "authTest.multiFetch"} {
		assertAccountParam(t, harness, toolName, "test_api_account", []string{"prod", "staging"})
	}

	// test_oauth has 1 account → auto-select, no param on any tool.
	for _, toolName := range []string{"authTest.get", "authTest.multiFetch"} {
		assertNoAccountParam(t, harness, toolName, "test_oauth_account")
	}
}
