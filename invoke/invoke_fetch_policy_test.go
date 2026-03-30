package invoke

import (
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
	}}, nil)
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
		}}, nil)
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
		}}, nil)
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
}
