package invoke_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

// loadAuthTestFixture loads the auth-test fixture package and builds injection rules.
func loadAuthTestFixture(t *testing.T) (assembler.LoadedPackage, []transport.InjectionRule) {
	t.Helper()
	loadedPkgs, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("auth-test"))
	if err != nil {
		t.Fatalf("assembler.Load: %v", err)
	}
	loaded := loadedPkgs.Packages[0]
	rules := credentialrepo.BuildInjectionRules(loaded.Package.Package)
	if len(rules) != 2 {
		t.Fatalf("expected 2 injection rules (test_api + test_oauth), got %d", len(rules))
	}
	return loaded, rules
}

// overrideOAuth2TokenURL replaces the TokenURL in all OAuth2 rules with the given URL.
func overrideOAuth2TokenURL(rules []transport.InjectionRule, tokenURL string) {
	for i := range rules {
		if rules[i].Type == transport.CredentialTypeOAuth2 && rules[i].Provider != nil {
			rules[i].Provider.TokenURL = tokenURL
		}
	}
}

// TestMultiAccount_ExpiredRefreshToken_ErrorNoSecretLeak verifies that when
// an account's OAuth2 refresh token is missing or the token endpoint is broken,
// the error exposed to the tool is generic and does not leak secret paths.
func TestMultiAccount_ExpiredRefreshToken_ErrorNoSecretLeak(t *testing.T) {
	t.Parallel()

	loaded, rules := loadAuthTestFixture(t)
	module := loaded.Package.Package.Module.String()

	// Mock token endpoint: "work" succeeds, "personal" fails.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		refreshToken := r.FormValue("refresh_token")
		if refreshToken == "RT-WORK" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "ACCESS-WORK",
				"expires_in":   3600,
			})
			return
		}
		// All other refresh tokens fail.
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	t.Cleanup(tokenSrv.Close)

	overrideOAuth2TokenURL(rules, tokenSrv.URL+"/refresh")

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		// Shared client credentials.
		credpath.OAuth2ClientID(module, "test_oauth"):     []byte("CID"),
		credpath.OAuth2ClientSecret(module, "test_oauth"): []byte("CSEC"),
		// "work" account: valid refresh token.
		credpath.OAuth2RefreshToken(module, "test_oauth", "work"): []byte("RT-WORK"),
		// "personal" account: NO refresh token at all.
		// (The missing refresh token will cause a secret-not-found error.)
	})

	credAccounts, err := credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts: %v", err)
	}

	// work has a refresh_token key under accounts/work/, personal does not
	// since we didn't seed it. DiscoverCredentialAccounts only finds "work".
	// To test the error case, we need personal to appear as an account.
	// Seed a refresh_token for personal that will cause a token endpoint failure.
	store.Seed(map[string][]byte{
		credpath.OAuth2RefreshToken(module, "test_oauth", "personal"): []byte("RT-BROKEN"),
	})

	// Re-discover now that personal has a key.
	credAccounts, err = credentialrepo.DiscoverCredentialAccounts(
		context.Background(), loaded.Package.Package, store)
	if err != nil {
		t.Fatalf("DiscoverCredentialAccounts (retry): %v", err)
	}

	if len(credAccounts["test_oauth"]) != 2 {
		t.Fatalf("expected 2 accounts for test_oauth, got %v", credAccounts["test_oauth"])
	}

	ci := transport.NewCredentialInjector(rules, store, transport.WithRefreshClient(tokenSrv.Client()))

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("auth-test"), toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			loaded.Package.Package.Module: {
				CredentialAccounts: credAccounts,
				Injector:           ci,
			},
		},
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	// The test_oauth credential matches "localhost" hosts, so use localhost in the URL.
	oauthURL := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)

	// "work" account should succeed.
	t.Run("work_account_succeeds", func(t *testing.T) {
		result, err := invoke.Run(prepared, "authTest.get", map[string]any{
			"url":                oauthURL + "/secured/users",
			"test_oauth_account": "work",
		})
		if err != nil {
			t.Fatalf("invoke.Run with work account: %v", err)
		}
		if !strings.Contains(result, "ok") {
			t.Errorf("expected upstream response, got: %s", result)
		}
	})

	// "personal" account should fail because token endpoint rejects RT-BROKEN.
	t.Run("personal_account_fails", func(t *testing.T) {
		_, err := invoke.Run(prepared, "authTest.get", map[string]any{
			"url":                oauthURL + "/secured/users",
			"test_oauth_account": "personal",
		})
		if err == nil {
			t.Fatal("expected error for personal account with broken refresh token")
		}

		errMsg := err.Error()

		// Error exposed to tool should be generic.
		if !strings.Contains(errMsg, "credential injection failed") {
			t.Errorf("expected generic 'credential injection failed' error, got: %s", errMsg)
		}

		// Must NOT leak secret store paths.
		if strings.Contains(errMsg, "auth-test/test_oauth/accounts/personal") {
			t.Errorf("secret path leaked in error message: %s", errMsg)
		}
		if strings.Contains(errMsg, "RT-BROKEN") {
			t.Errorf("refresh token value leaked in error message: %s", errMsg)
		}
		if strings.Contains(errMsg, "CSEC") {
			t.Errorf("client secret leaked in error message: %s", errMsg)
		}
	})
}
