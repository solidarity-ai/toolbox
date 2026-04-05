package invoke_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestE2E_UsesPreparedToolInjector(t *testing.T) {
	t.Parallel()

	var gotAuth atomic.Value
	gotAuth.Store("")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "prepared-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	upstreamURL, _ := url.Parse(upstream.URL)

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.OAuth2ClientID("myapi", "default"):          []byte("cid"),
		credpath.OAuth2ClientSecret("myapi", "default"):      []byte("csec"),
		credpath.Shared("myapi", "default", "refresh_token"): []byte("rtok"),
	})

	ci := transport.NewCredentialInjector(
		[]transport.InjectionRule{{
			Hosts:                    []string{upstreamURL.Hostname()},
			ModuleName:               "myapi",
			CredentialName:           "default",
			SecretPrefix:             credpath.SharedPrefix("myapi", "default"),
			Type:                     transport.CredentialTypeOAuth2,
			Method:                   transport.InjectionMethodBearerHeader,
			AllowUnsafeHTTPInjection: true,
			Provider:                 &transport.OAuth2Provider{TokenURL: tokenSrv.URL + "/token"},
		}},
		store,
		transport.WithRefreshClient(tokenSrv.Client()),
	)

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("fetch-test"), toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			tooldef.ModulePath("fixtures.local/fetch-test"): {
				Injector: ci,
			},
		},
	})

	result, err := invoke.Run(prepared, "fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	if err != nil {
		t.Fatalf("invoke.Run returned error: %v", err)
	}

	if !strings.Contains(result, "ok") {
		t.Fatalf("expected ok in result, got: %s", result)
	}

	auth := gotAuth.Load().(string)
	if auth != "Bearer prepared-token" {
		t.Fatalf("upstream got Authorization %q, want %q", auth, "Bearer prepared-token")
	}
}

func TestE2E_UsesPreparedToolAllowlist(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("fetch-test"), toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			tooldef.ModulePath("fixtures.local/fetch-test"): {
				Allowlist: transport.NewHostAllowlist([]string{"blocked.example.com"}),
			},
		},
	})

	_, err := invoke.Run(prepared, "fetchTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	if !strings.Contains(err.Error(), "not in allowlist") {
		t.Fatalf("expected allowlist failure, got: %v", err)
	}
}
