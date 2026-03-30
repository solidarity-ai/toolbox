package transport_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestInjectorExactHostBeatsWildcard(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/exact":    []byte("exact-token"),
		"pkg/wildcard": []byte("wildcard-token"),
	}},
		transport.Rule{
			Name:      "wildcard",
			SecretKey: "pkg/wildcard",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"*.github.com"},
				Method: "bearer_header",
			},
		},
		transport.Rule{
			Name:      "exact",
			SecretKey: "pkg/exact",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		},
	)

	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer exact-token"}}, headers.Entries()); diff != "" {
		t.Fatalf("headers mismatch (-want +got):\n%s", diff)
	}
}

func TestInjectorLongerPathPrefixBeatsShorterOnSameHost(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/root": []byte("root-token"),
		"pkg/deep": []byte("deep-token"),
	}},
		transport.Rule{
			Name:      "root",
			SecretKey: "pkg/root",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos",
				Method:     "bearer_header",
			},
		},
		transport.Rule{
			Name:      "deep",
			SecretKey: "pkg/deep",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos/private",
				Method:     "bearer_header",
			},
		},
	)

	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/private/secret", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer deep-token"}}, headers.Entries()); diff != "" {
		t.Fatalf("headers mismatch (-want +got):\n%s", diff)
	}
}

func TestInjectorWildcardDoesNotMatchBareParentHost(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/wildcard": []byte("wildcard-token"),
	}}, transport.Rule{
		Name:      "wildcard",
		SecretKey: "pkg/wildcard",
		Type:      tooldef.CredentialTypeBearer,
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"*.googleapis.com"},
			Method: "bearer_header",
		},
	})

	headers := fetch.NewHeaders()
	gotURL, err := injector.InjectRequest(context.Background(), "https://googleapis.com/discovery/v1/apis", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if gotURL != "https://googleapis.com/discovery/v1/apis" {
		t.Fatalf("rewritten url = %q, want original", gotURL)
	}
	if len(headers.Entries()) != 0 {
		t.Fatalf("headers = %v, want no injection", headers.Entries())
	}
}

func TestInjectorBuiltInMethods(t *testing.T) {
	t.Parallel()

	t.Run("basic_auth emits one authorization header", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/basic": []byte("octocat:secret")}}, transport.Rule{
			Name:      "basic",
			SecretKey: "pkg/basic",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "basic_auth",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("octocat:secret"))
		if diff := cmp.Diff([][2]string{{"authorization", want}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("api_key_header uses configured header name", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/header": []byte("header-token")}}, transport.Rule{
			Name:      "header",
			SecretKey: "pkg/header",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.example.com"},
				Method:     "api_key_header",
				HeaderName: "X-Custom-Key",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.example.com/data", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if diff := cmp.Diff([][2]string{{"x-custom-key", "header-token"}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("api_key_query uses configured query name", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/query": []byte("query-token")}}, transport.Rule{
			Name:      "query",
			SecretKey: "pkg/query",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:     []string{"api.example.com"},
				Method:    "api_key_query",
				QueryName: "api_key",
			},
		})

		headers := fetch.NewHeaders()
		gotURL, err := injector.InjectRequest(context.Background(), "https://api.example.com/data?existing=1", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if gotURL != "https://api.example.com/data?api_key=query-token&existing=1" && gotURL != "https://api.example.com/data?existing=1&api_key=query-token" {
			t.Fatalf("rewritten url = %q, want query injection", gotURL)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no header mutation", headers.Entries())
		}
	})

	t.Run("oauth2 reads reserved access_token family key", func(t *testing.T) {
		t.Parallel()
		const accessTokenKey = "github.com/example/github-issues/github_oauth/access_token"
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{accessTokenKey: []byte("oauth-token")}}, transport.Rule{
			Name:      "github_oauth",
			SecretKey: accessTokenKey,
			Type:      tooldef.CredentialTypeOAuth2,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if diff := cmp.Diff([][2]string{{"authorization", "Bearer oauth-token"}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestInjectorRejectsMalformedInputsAtConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule transport.Rule
		want string
	}{
		{
			name: "empty host pattern",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"   "}},
			},
			want: "host pattern must not be empty",
		},
		{
			name: "unsupported method",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "digest_auth"},
			},
			want: "unsupported injection method",
		},
		{
			name: "api key header requires header name",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeAPIKey,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "api_key_header"},
			},
			want: "headerName must not be empty",
		},
		{
			name: "api key query requires query name",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeAPIKey,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "api_key_query"},
			},
			want: "queryName must not be empty",
		},
		{
			name: "malformed path prefix",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, PathPrefix: "repos"},
			},
			want: "path prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := transport.NewInjector(nil, []transport.Rule{tt.rule})
			if err == nil {
				t.Fatal("expected NewInjector to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewInjector error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestInjectorRejectsAmbiguousCanonicalRules(t *testing.T) {
	t.Parallel()

	_, err := transport.NewInjector(nil, []transport.Rule{
		{
			Name:      "first",
			SecretKey: "pkg/first",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{" API.GITHUB.COM "},
				PathPrefix: "/repos/",
				Method:     "bearer_header",
			},
		},
		{
			Name:      "second",
			SecretKey: "pkg/second",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos",
				Method:     "bearer_header",
			},
		},
	})
	if err == nil {
		t.Fatal("expected NewInjector to reject ambiguous canonical rules")
	}
	if !strings.Contains(err.Error(), "ambiguous transport credential rules") {
		t.Fatalf("NewInjector error = %v, want ambiguity context", err)
	}
}

func TestInjectorReportsMissingSecretStoreAndSecretValueFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing secret store", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, nil, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected missing secret store error")
		}
		if !strings.Contains(err.Error(), "no secret store") {
			t.Fatalf("InjectRequest error = %v, want missing store context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("missing secret value", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{err: secrets.ErrNotFound}, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected missing secret error")
		}
		if !strings.Contains(err.Error(), "resolve transport credential \"pkg/token\"") {
			t.Fatalf("InjectRequest error = %v, want secret lookup context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("unusable secret material", func(t *testing.T) {
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/token": []byte("   ")}}, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected unusable secret material error")
		}
		if !strings.Contains(err.Error(), "resolved unusable secret material") {
			t.Fatalf("InjectRequest error = %v, want unusable secret context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("basic auth rejects malformed secret material", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/basic": []byte("missing-colon")}}, transport.Rule{
			Name:      "basic",
			SecretKey: "pkg/basic",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "basic_auth"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err == nil {
			t.Fatal("expected malformed basic_auth secret error")
		}
		if !strings.Contains(err.Error(), "malformed basic_auth secret material") {
			t.Fatalf("InjectRequest error = %v, want malformed basic_auth context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})
}

func TestInjectorRejectsInvalidRequestURLs(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/token": []byte("token")}}, transport.Rule{
		Name:      "token",
		SecretKey: "pkg/token",
		Type:      tooldef.CredentialTypeBearer,
		Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
	})

	_, err := injector.InjectRequest(context.Background(), "://bad-url", fetch.NewHeaders())
	if err == nil {
		t.Fatal("expected invalid url error")
	}
	if !strings.Contains(err.Error(), "parse request url") {
		t.Fatalf("InjectRequest error = %v, want parse context", err)
	}
}

func TestInjectorOAuth2RefreshCachesAccessTokenAndAvoidsRepeatProviderCalls(t *testing.T) {
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
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("refresh request unexpectedly carried auth header %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		if diff := cmp.Diff(url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {"client-123"},
			"client_secret": {"secret-123"},
			"refresh_token": {"refresh-123"},
		}, values); diff != "" {
			t.Fatalf("refresh form mismatch (-want +got):\n%s", diff)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"cached-oauth-token","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	clock := &mutableClock{now: time.Unix(1_700_000_000, 0)}
	injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithClock(clock.Now), transport.WithRefreshHTTPClient(tokenServer.Client()))

	firstHeaders := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", firstHeaders)
	if err != nil {
		t.Fatalf("first InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer cached-oauth-token"}}, firstHeaders.Entries()); diff != "" {
		t.Fatalf("first headers mismatch (-want +got):\n%s", diff)
	}

	secondHeaders := fetch.NewHeaders()
	_, err = injector.InjectRequest(context.Background(), "https://api.github.com/user", secondHeaders)
	if err != nil {
		t.Fatalf("second InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer cached-oauth-token"}}, secondHeaders.Entries()); diff != "" {
		t.Fatalf("second headers mismatch (-want +got):\n%s", diff)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1 cache-backed refresh", got)
	}
}

func TestInjectorOAuth2RefreshExpiryBufferAndSecretRotation(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-123",
		"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
	})

	var (
		mu          sync.Mutex
		refreshSeen []url.Values
	)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		mu.Lock()
		refreshSeen = append(refreshSeen, values)
		callIndex := len(refreshSeen)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if callIndex == 1 {
			_, _ = io.WriteString(w, `{"access_token":"token-one","expires_in":120}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"token-two","expires_in":120}`)
	}))
	defer tokenServer.Close()

	clock := &mutableClock{now: time.Unix(1_700_000_000, 0)}
	injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithClock(clock.Now), transport.WithRefreshHTTPClient(tokenServer.Client()))

	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
	if err != nil {
		t.Fatalf("initial InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer token-one"}}, headers.Entries()); diff != "" {
		t.Fatalf("initial headers mismatch (-want +got):\n%s", diff)
	}

	clock.Set(clock.now.Add(59 * time.Second))
	headers = fetch.NewHeaders()
	_, err = injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
	if err != nil {
		t.Fatalf("outside-buffer InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer token-one"}}, headers.Entries()); diff != "" {
		t.Fatalf("outside-buffer headers mismatch (-want +got):\n%s", diff)
	}

	store.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-rotated",
		"github.com/example/github-issues/github_oauth/client_secret": "secret-rotated",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-rotated",
	})
	clock.Set(time.Unix(1_700_000_000, 0).Add(60 * time.Second))
	headers = fetch.NewHeaders()
	_, err = injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
	if err != nil {
		t.Fatalf("inside-buffer InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer token-two"}}, headers.Entries()); diff != "" {
		t.Fatalf("inside-buffer headers mismatch (-want +got):\n%s", diff)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(refreshSeen) != 2 {
		t.Fatalf("refresh calls = %d, want 2 total refreshes", len(refreshSeen))
	}
	if got := refreshSeen[1].Get("client_id"); got != "client-rotated" {
		t.Fatalf("rotated client_id = %q, want client-rotated", got)
	}
	if got := refreshSeen[1].Get("client_secret"); got != "secret-rotated" {
		t.Fatalf("rotated client_secret = %q, want secret-rotated", got)
	}
	if got := refreshSeen[1].Get("refresh_token"); got != "refresh-rotated" {
		t.Fatalf("rotated refresh_token = %q, want refresh-rotated", got)
	}
}

func TestInjectorOAuth2RefreshSupportsPublicClientsWithoutClientSecret(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-public",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-public",
	})

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		if got := values.Get("client_secret"); got != "" {
			t.Fatalf("client_secret = %q, want omitted for public client", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"public-token","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithRefreshHTTPClient(tokenServer.Client()))
	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer public-token"}}, headers.Entries()); diff != "" {
		t.Fatalf("headers mismatch (-want +got):\n%s", diff)
	}
}

func TestInjectorOAuth2RefreshFailsClosedWithActionableRedactedErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing refresh secret", func(t *testing.T) {
		t.Parallel()
		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id": "client-123",
		})
		injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule("https://auth.example.invalid/token")})
		headers := fetch.NewHeaders()
		gotURL, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err == nil {
			t.Fatal("expected secret reread failure")
		}
		if gotURL != "https://api.github.com/user" {
			t.Fatalf("returned url = %q, want original url on preflight failure", gotURL)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no injection on failure", headers.Entries())
		}
		if !strings.Contains(err.Error(), "during secret reread") || !strings.Contains(err.Error(), "re-authorize") {
			t.Fatalf("error = %v, want secret reread stage and operator guidance", err)
		}
		if strings.Contains(err.Error(), "client-123") {
			t.Fatalf("error leaked secret material: %v", err)
		}
	})

	t.Run("provider 500", func(t *testing.T) {
		t.Parallel()
		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer tokenServer.Close()

		injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithRefreshHTTPClient(tokenServer.Client()))
		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err == nil {
			t.Fatal("expected token request failure")
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no injection on token request failure", headers.Entries())
		}
		if !strings.Contains(err.Error(), "during token request") || !strings.Contains(err.Error(), "status 502") {
			t.Fatalf("error = %v, want token request stage and upstream status", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"late-token","expires_in":3600}`)
		}))
		defer tokenServer.Close()

		client := tokenServer.Client()
		client.Timeout = 10 * time.Millisecond
		injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithRefreshHTTPClient(client))
		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err == nil {
			t.Fatal("expected timeout failure")
		}
		if !strings.Contains(err.Error(), "during token request") {
			t.Fatalf("error = %v, want token request stage", err)
		}
	})
}

func TestInjectorOAuth2RefreshRejectsMalformedResponses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed json", body: `{"access_token":`, want: "response parse"},
		{name: "missing access token", body: `{"expires_in":3600}`, want: "access_token is required"},
		{name: "non numeric expiry", body: `{"access_token":"token","expires_in":"soon"}`, want: "expires_in must be numeric"},
		{name: "negative expiry", body: `{"access_token":"token","expires_in":-1}`, want: "expires_in must be positive"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := testutil.NewTestSecretStore()
			store.SeedStrings(map[string]string{
				"github.com/example/github-issues/github_oauth/client_id":     "client-123",
				"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
				"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
			})
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer tokenServer.Close()

			injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithRefreshHTTPClient(tokenServer.Client()))
			headers := fetch.NewHeaders()
			_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
			if err == nil {
				t.Fatal("expected malformed response failure")
			}
			if len(headers.Entries()) != 0 {
				t.Fatalf("headers = %v, want no injection on parse failure", headers.Entries())
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestInjectorOAuth2SingleflightSharesOneInFlightRefresh(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-123",
		"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
	})

	var tokenCalls atomic.Int32
	release := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"shared-token","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	injector := newInjectorWithOptions(t, store, []transport.Rule{oauth2Rule(tokenServer.URL)}, transport.WithRefreshHTTPClient(tokenServer.Client()))

	const callers = 8
	results := make(chan error, callers)
	started := make(chan struct{}, callers)
	for range callers {
		go func() {
			started <- struct{}{}
			headers := fetch.NewHeaders()
			_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
			if err == nil {
				if diff := cmp.Diff([][2]string{{"authorization", "Bearer shared-token"}}, headers.Entries()); diff != "" {
					results <- errors.New(diff)
					return
				}
			}
			results <- err
		}()
	}
	for range callers {
		<-started
	}
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent InjectRequest: %v", err)
		}
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1 shared refresh", got)
	}
}

func TestNormalizeAllowedHosts(t *testing.T) {
	t.Parallel()

	got := transport.NormalizeAllowedHosts([]string{" *.googleapis.com ", "oauth2.googleapis.com", "oauth2.googleapis.com", ""})
	want := []string{"*.googleapis.com", "oauth2.googleapis.com"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("NormalizeAllowedHosts mismatch (-want +got):\n%s", diff)
	}
}

func oauth2Rule(tokenURL string) transport.Rule {
	return transport.Rule{
		Name:               "github_oauth",
		SecretKey:          "github.com/example/github-issues/github_oauth/access_token",
		Type:               tooldef.CredentialTypeOAuth2,
		OAuth2Provider:     &tooldef.OAuth2ProviderConfig{TokenURL: tokenURL, AuthURL: "https://accounts.example.com/oauth/authorize"},
		OAuth2SecretFamily: "github.com/example/github-issues/github_oauth",
		OAuth2CacheKey:     "github.com/example/github-issues:github_oauth",
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"api.github.com"},
			Method: "bearer_header",
		},
	}
}

type mutableClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *mutableClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func newInjector(t testing.TB, store secrets.SecretStore, rules ...transport.Rule) *transport.Injector {
	t.Helper()
	injector, err := transport.NewInjector(store, rules)
	if err != nil {
		t.Fatalf("NewInjector: %v", err)
	}
	return injector
}

func newInjectorWithOptions(t testing.TB, store secrets.SecretStore, rules []transport.Rule, opts ...transport.InjectorOption) *transport.Injector {
	t.Helper()
	injector, err := transport.NewInjectorWithOptions(store, rules, opts...)
	if err != nil {
		t.Fatalf("NewInjectorWithOptions: %v", err)
	}
	return injector
}

type stubSecretStore struct {
	values map[string][]byte
	err    error
}

func (s stubSecretStore) Get(_ context.Context, key string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return nil, secrets.ErrNotFound
}

func (s stubSecretStore) Set(context.Context, string, []byte) error {
	return errors.New("not implemented")
}

func (s stubSecretStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func (s stubSecretStore) List(context.Context, string) ([]string, error) {
	return nil, errors.New("not implemented")
}
