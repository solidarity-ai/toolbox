package invoke

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestGoFetchWithTransportPolicySurfacesPolicyErrorsBeforeOutboundFetch(t *testing.T) {
	t.Parallel()

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": "test-token",
	})

	policy, err := transport.NewPolicy(secretStore, []transport.Rule{{
		Name:      "github_token",
		SecretKey: "github.com/example/github-issues/github_token",
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"api.github.com"},
			Method: "bearer_header",
		},
	}}, []string{"api.github.com"}, false)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}

	fetchFn := goFetchWithTransportPolicy(policy)

	t.Run("invalid url still evaluates transport policy boundary", func(t *testing.T) {
		_, err := fetchFn("://bad-url", "GET", "not-json", "")
		if err == nil {
			t.Fatal("expected invalid url to fail")
		}
		if !strings.Contains(err.Error(), "parse request url") {
			t.Fatalf("error = %v, want parse request url context", err)
		}
	})

	t.Run("prepare request errors short circuit before outbound fetch", func(t *testing.T) {
		var requestCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		parsedURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}

		policy, err := transport.NewPolicy(nil, []transport.Rule{{
			Name:      "github_token",
			SecretKey: "github.com/example/github-issues/github_token",
			Inject: tooldef.CredentialInject{
				Hosts:  []string{parsedURL.Hostname()},
				Method: "bearer_header",
			},
		}}, []string{parsedURL.Hostname()}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}

		_, err = goFetchWithTransportPolicy(policy)(srv.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected transport policy to fail before outbound fetch")
		}
		if !strings.Contains(err.Error(), "no secret store") {
			t.Fatalf("error = %v, want missing secret store context", err)
		}
		if got := requestCount.Load(); got != 0 {
			t.Fatalf("request count = %d, want 0 outbound requests when policy preparation fails", got)
		}
	})

	t.Run("oauth2 family lookup failures short circuit before outbound fetch", func(t *testing.T) {
		var requestCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		parsedURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}

		policy, err := transport.NewPolicy(testutil.NewTestSecretStore(), []transport.Rule{{
			Name:      "github_oauth",
			SecretKey: "github.com/example/github-issues/github_oauth/access_token",
			Type:      tooldef.CredentialTypeOAuth2,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{parsedURL.Hostname()},
				Method: "bearer_header",
			},
		}}, []string{parsedURL.Hostname()}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}

		_, err = goFetchWithTransportPolicy(policy)(srv.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected missing oauth2 access token to fail before outbound fetch")
		}
		if !strings.Contains(err.Error(), "github_oauth/access_token") {
			t.Fatalf("error = %v, want reserved access token key context", err)
		}
		if got := requestCount.Load(); got != 0 {
			t.Fatalf("request count = %d, want 0 outbound requests when oauth2 lookup fails", got)
		}
	})

	t.Run("deny by default short circuits before outbound fetch", func(t *testing.T) {
		var requestCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		policy, err := transport.NewPolicy(nil, nil, nil, true)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}

		_, err = goFetchWithTransportPolicy(policy)(srv.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected deny-by-default policy to reject request")
		}
		if !strings.Contains(err.Error(), "no allowed hosts declared") {
			t.Fatalf("error = %v, want deny-by-default context", err)
		}
		if got := requestCount.Load(); got != 0 {
			t.Fatalf("request count = %d, want 0 outbound requests when allowlist denies", got)
		}
	})

	t.Run("explicit denied hosts short circuit before outbound fetch", func(t *testing.T) {
		var requestCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		policy, err := transport.NewPolicy(nil, nil, []string{"example.invalid"}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}

		_, err = goFetchWithTransportPolicy(policy)(srv.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected explicit host allowlist to reject request")
		}
		if !strings.Contains(err.Error(), "not allowed by policy") {
			t.Fatalf("error = %v, want explicit denied-host context", err)
		}
		if got := requestCount.Load(); got != 0 {
			t.Fatalf("request count = %d, want 0 outbound requests when allowlist denies", got)
		}
	})

	t.Run("allowed matching hosts still reach fetch seam", func(t *testing.T) {
		var requestCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("Authorization header = %q, want injected bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		parsedURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}

		policy, err := transport.NewPolicy(secretStore, []transport.Rule{{
			Name:      "github_token",
			SecretKey: "github.com/example/github-issues/github_token",
			Inject: tooldef.CredentialInject{
				Hosts:  []string{parsedURL.Hostname()},
				Method: "bearer_header",
			},
		}}, []string{parsedURL.Hostname()}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}

		result, err := goFetchWithTransportPolicy(policy)(srv.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err != nil {
			t.Fatalf("goFetchWithTransportPolicy: %v", err)
		}
		if got := requestCount.Load(); got != 1 {
			t.Fatalf("request count = %d, want exactly 1 outbound request", got)
		}
		if result.Status != http.StatusOK {
			t.Fatalf("status = %d, want %d", result.Status, http.StatusOK)
		}
		if result.Body != `{"ok":true}` {
			t.Fatalf("body = %q, want JSON response", result.Body)
		}
	})
}

func TestGoFetchWithTransportPolicyOAuth2RefreshParity(t *testing.T) {
	t.Parallel()

	t.Run("refreshes before outbound fetch and reuses cached access token", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})

		var tokenCalls atomic.Int32
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if got := values.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", got)
			}
			if got := values.Get("client_id"); got != "client-123" {
				t.Fatalf("client_id = %q, want client-123", got)
			}
			if got := values.Get("client_secret"); got != "secret-123" {
				t.Fatalf("client_secret = %q, want secret-123", got)
			}
			if got := values.Get("refresh_token"); got != "refresh-123" {
				t.Fatalf("refresh_token = %q, want refresh-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		}))
		defer tokenServer.Close()

		var apiHits atomic.Int32
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("Authorization header = %q, want refreshed bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer apiServer.Close()

		fetchFn := goFetchWithTransportPolicy(mustOAuth2FetchPolicy(t, store, tokenServer.URL, apiServer.URL))

		for attempt := range 2 {
			result, err := fetchFn(apiServer.URL+"/repos/octocat/hello-world", "GET", "[]", "")
			if err != nil {
				t.Fatalf("attempt %d goFetchWithTransportPolicy: %v", attempt+1, err)
			}
			if result.Status != http.StatusOK {
				t.Fatalf("attempt %d status = %d, want %d", attempt+1, result.Status, http.StatusOK)
			}
		}

		if got := apiHits.Load(); got != 2 {
			t.Fatalf("protected upstream hits = %d, want 2", got)
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1 cached refresh", got)
		}
	})

	t.Run("refresh failure aborts before protected upstream fetch and stays redacted", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})

		var tokenCalls atomic.Int32
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			http.Error(w, "upstream denied", http.StatusBadGateway)
		}))
		defer tokenServer.Close()

		var apiHits atomic.Int32
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer apiServer.Close()

		fetchFn := goFetchWithTransportPolicy(mustOAuth2FetchPolicy(t, store, tokenServer.URL, apiServer.URL))
		_, err := fetchFn(apiServer.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected refresh failure before protected upstream fetch")
		}
		if !strings.Contains(err.Error(), "during token request") || !strings.Contains(err.Error(), "status 502") {
			t.Fatalf("error = %v, want token-request failure context", err)
		}
		if strings.Contains(err.Error(), "refreshed-token") || strings.Contains(err.Error(), "secret-123") {
			t.Fatalf("error leaked auth material: %v", err)
		}
		if got := apiHits.Load(); got != 0 {
			t.Fatalf("protected upstream hits = %d, want 0", got)
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
	})

	t.Run("malformed refresh response aborts before protected upstream fetch", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})

		var tokenCalls atomic.Int32
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"expires_in":3600}`))
		}))
		defer tokenServer.Close()

		var apiHits atomic.Int32
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer apiServer.Close()

		fetchFn := goFetchWithTransportPolicy(mustOAuth2FetchPolicy(t, store, tokenServer.URL, apiServer.URL))
		_, err := fetchFn(apiServer.URL+"/repos/octocat/hello-world", "GET", "[]", "")
		if err == nil {
			t.Fatal("expected malformed refresh payload to fail")
		}
		if !strings.Contains(err.Error(), "response parse") || !strings.Contains(err.Error(), "access_token is required") {
			t.Fatalf("error = %v, want parse failure context", err)
		}
		if got := apiHits.Load(); got != 0 {
			t.Fatalf("protected upstream hits = %d, want 0", got)
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
	})
}

func mustOAuth2FetchPolicy(t *testing.T, store *testutil.TestSecretStore, tokenURL string, protectedURL string) *transport.Policy {
	t.Helper()

	parsedProtectedURL, err := url.Parse(protectedURL)
	if err != nil {
		t.Fatalf("parse protected url: %v", err)
	}

	policy, err := transport.NewPolicy(store, []transport.Rule{{
		Name:               "github_oauth",
		SecretKey:          "github.com/example/github-issues/github_oauth/access_token",
		Type:               tooldef.CredentialTypeOAuth2,
		OAuth2Provider:     &tooldef.OAuth2ProviderConfig{AuthURL: "https://accounts.example.com/oauth/authorize", TokenURL: tokenURL},
		OAuth2SecretFamily: "github.com/example/github-issues/github_oauth",
		OAuth2CacheKey:     "github.com/example/github-issues:github_oauth",
		Inject: tooldef.CredentialInject{
			Hosts:  []string{parsedProtectedURL.Hostname()},
			Method: "bearer_header",
		},
	}}, []string{parsedProtectedURL.Hostname()}, false)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	if policy == nil {
		t.Fatal("expected runtime policy")
	}
	return policy
}
