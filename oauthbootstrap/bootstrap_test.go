package oauthbootstrap_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestBootstrapPKCEPublicClientUsesChallengeAndVerifier(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	provider, tokenServer := newOAuthTestProvider(t, func(t *testing.T, _ http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		if got := values.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := values.Get("client_secret"); got != "" {
			t.Fatalf("client_secret = %q, want omitted for public client", got)
		}
		if got := values.Get("code_verifier"); got == "" {
			t.Fatal("expected code_verifier for PKCE public client")
		}
	})
	defer tokenServer.Close()

	var opened string
	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			opened = authURL
			return completeCallback(t, authURL, "auth-code", "")
		},
		CallbackTimeout: 2 * time.Second,
	})

	result, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:      singleCredentialPackage(provider, "github_oauth", []string{"repo", "user:email"}),
		ClientID:     "client-public",
		ClientSecret: "",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.UsedPKCE {
		t.Fatal("UsedPKCE = false, want true for public client")
	}

	parsed, err := url.Parse(opened)
	if err != nil {
		t.Fatalf("Parse(auth url): %v", err)
	}
	query := parsed.Query()
	if got := query.Get("scope"); got != "repo user:email" {
		t.Fatalf("scope = %q, want joined scopes", got)
	}
	if got := query.Get("code_challenge"); got == "" {
		t.Fatal("code_challenge missing from public-client auth url")
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	assertStoredKeys(t, store, map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-public",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-xyz",
	})
	assertSecretAbsent(t, store, "github.com/example/github-issues/github_oauth/access_token")
	assertSecretAbsent(t, store, "github.com/example/github-issues/github_oauth/client_secret")
}

func TestBootstrapCredentialSelectionFailures(t *testing.T) {
	t.Parallel()

	t.Run("no oauth2 credential", func(t *testing.T) {
		t.Parallel()
		bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
			Store:       testutil.NewTestSecretStore(),
			OpenBrowser: func(context.Context, string) error { t.Fatal("unexpected browser launch"); return nil },
		})
		_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
			Package: packaging.LoadedPackage{Package: tooldef.Package{
				Name:   "github-issues",
				Module: tooldef.ModulePath("github.com/example/github-issues"),
				Credentials: []tooldef.PackageCredential{{
					Name: "api_key",
					Type: tooldef.CredentialTypeAPIKey,
				}},
			}},
			ClientID: "unused",
		})
		if err == nil {
			t.Fatal("expected credential selection error")
		}
		if !strings.Contains(err.Error(), "during credential selection") || !strings.Contains(err.Error(), "declares no oauth2 credentials") {
			t.Fatalf("error = %v, want credential-selection context", err)
		}
	})

	t.Run("ambiguous oauth2 credential", func(t *testing.T) {
		bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
			Store:       testutil.NewTestSecretStore(),
			OpenBrowser: func(context.Context, string) error { t.Fatal("unexpected browser launch"); return nil },
		})
		provider := oauthProviderRefFromServer(newNoopTLSServer(t).URL)
		_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
			Package: packaging.LoadedPackage{Package: tooldef.Package{
				Name:   "github-issues",
				Module: tooldef.ModulePath("github.com/example/github-issues"),
				Credentials: []tooldef.PackageCredential{
					{Name: "github_primary", Type: tooldef.CredentialTypeOAuth2, Provider: provider, Scopes: []string{"repo"}},
					{Name: "github_backup", Type: tooldef.CredentialTypeOAuth2, Provider: provider, Scopes: []string{"repo"}},
				},
			}},
			ClientID: "client-123",
		})
		if err == nil {
			t.Fatal("expected ambiguous credential error")
		}
		if !strings.Contains(err.Error(), "multiple oauth2 credentials") || !strings.Contains(err.Error(), "github_backup") {
			t.Fatalf("error = %v, want ambiguous credential names", err)
		}
	})
}

func TestBootstrapCallbackValidationFailureIsRedacted(t *testing.T) {
	t.Parallel()

	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      testutil.NewTestSecretStore(),
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "wrong-state")
		},
		CallbackTimeout: 2 * time.Second,
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID: "client-public",
	})
	if err == nil {
		t.Fatal("expected callback validation error")
	}
	if !strings.Contains(err.Error(), "during callback validation") || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("error = %v, want callback validation mismatch", err)
	}
	if strings.Contains(err.Error(), "auth-code") || strings.Contains(err.Error(), "client-public") {
		t.Fatalf("error leaked secret or callback material: %v", err)
	}
}

func TestBootstrapCallbackTimeoutIncludesRedirectURI(t *testing.T) {
	t.Parallel()

	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:           testutil.NewTestSecretStore(),
		HTTPClient:      tokenServer.Client(),
		OpenBrowser:     func(context.Context, string) error { return nil },
		CallbackTimeout: 50 * time.Millisecond,
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID: "client-public",
	})
	if err == nil {
		t.Fatal("expected callback timeout")
	}
	if !strings.Contains(err.Error(), "during callback wait") || !strings.Contains(err.Error(), "http://127.0.0.1:") {
		t.Fatalf("error = %v, want callback timeout with redirect uri", err)
	}
}

func TestBootstrapCredentialSelectionRejectsBlankClientID(t *testing.T) {
	t.Parallel()

	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      testutil.NewTestSecretStore(),
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(context.Context, string) error {
			t.Fatal("unexpected browser launch")
			return nil
		},
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID: "  ",
	})
	if err == nil {
		t.Fatal("expected blank client_id error")
	}
	if !strings.Contains(err.Error(), "during credential selection") || !strings.Contains(err.Error(), "client_id must not be empty") {
		t.Fatalf("error = %v, want client_id validation", err)
	}
}

func TestBootstrapCredentialSelectionRejectsMissingProviderScopesAndModule(t *testing.T) {
	t.Parallel()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:       testutil.NewTestSecretStore(),
		OpenBrowser: func(context.Context, string) error { t.Fatal("unexpected browser launch"); return nil },
	})

	t.Run("missing module", func(t *testing.T) {
		_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
			Package: packaging.LoadedPackage{Package: tooldef.Package{
				Name: "github-issues",
				Credentials: []tooldef.PackageCredential{{
					Name:     "github_oauth",
					Type:     tooldef.CredentialTypeOAuth2,
					Provider: tooldef.OAuth2ProviderRef{Name: "google"},
					Scopes:   []string{"repo"},
				}},
			}},
			ClientID: "client-123",
		})
		if err == nil || !strings.Contains(err.Error(), "module identity is required") {
			t.Fatalf("error = %v, want missing module identity", err)
		}
	})

	t.Run("missing scopes", func(t *testing.T) {
		_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
			Package: packaging.LoadedPackage{Package: tooldef.Package{
				Name:   "github-issues",
				Module: tooldef.ModulePath("github.com/example/github-issues"),
				Credentials: []tooldef.PackageCredential{{
					Name:     "github_oauth",
					Type:     tooldef.CredentialTypeOAuth2,
					Provider: tooldef.OAuth2ProviderRef{Name: "google"},
				}},
			}},
			ClientID: "client-123",
		})
		if err == nil || !strings.Contains(err.Error(), "must declare at least one scope") {
			t.Fatalf("error = %v, want missing scopes", err)
		}
	})

	t.Run("missing provider", func(t *testing.T) {
		_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
			Package: packaging.LoadedPackage{Package: tooldef.Package{
				Name:   "github-issues",
				Module: tooldef.ModulePath("github.com/example/github-issues"),
				Credentials: []tooldef.PackageCredential{{
					Name:   "github_oauth",
					Type:   tooldef.CredentialTypeOAuth2,
					Scopes: []string{"repo"},
				}},
			}},
			ClientID: "client-123",
		})
		if err == nil || !strings.Contains(err.Error(), "provider") {
			t.Fatalf("error = %v, want provider validation", err)
		}
	})
}

func newOAuthTestProvider(t *testing.T, verify func(*testing.T, http.ResponseWriter, *http.Request)) (tooldef.OAuth2ProviderRef, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if verify != nil {
			verify(t, w, r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-xyz","refresh_token":"refresh-xyz","expires_in":3600}`)
	}))
	provider := oauthProviderRefFromServer(server.URL)
	return provider, server
}

func oauthProviderRefFromServer(baseURL string) tooldef.OAuth2ProviderRef {
	return tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
		AuthURL:  baseURL + "/authorize",
		TokenURL: baseURL + "/token",
	}}
}

func completeCallback(t *testing.T, authURL string, code string, overrideState string) error {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		return fmt.Errorf("parse auth url: %w", err)
	}
	query := parsed.Query()
	redirectURI := query.Get("redirect_uri")
	state := query.Get("state")
	if overrideState != "" {
		state = overrideState
	}
	callbackURL := redirectURI + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	resp, err := http.Get(callbackURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return nil
}

func singleCredentialPackage(provider tooldef.OAuth2ProviderRef, name string, scopes []string) packaging.LoadedPackage {
	return packaging.LoadedPackage{Package: tooldef.Package{
		Name:   "github-issues",
		Module: tooldef.ModulePath("github.com/example/github-issues"),
		Credentials: []tooldef.PackageCredential{{
			Name:     name,
			Type:     tooldef.CredentialTypeOAuth2,
			Provider: provider,
			Scopes:   append([]string(nil), scopes...),
		}},
	}}
}

func assertStoredKeys(t *testing.T, store *testutil.TestSecretStore, want map[string]string) {
	t.Helper()
	for key, expected := range want {
		got, err := store.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		if diff := cmp.Diff(expected, string(got)); diff != "" {
			t.Fatalf("secret %q mismatch (-want +got):\n%s", key, diff)
		}
	}
}

func assertSecretAbsent(t *testing.T, store *testutil.TestSecretStore, key string) {
	t.Helper()
	_, err := store.Get(context.Background(), key)
	if err == nil {
		t.Fatalf("Get(%q) unexpectedly succeeded", key)
	}
}

func newNoopTLSServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	return server
}
