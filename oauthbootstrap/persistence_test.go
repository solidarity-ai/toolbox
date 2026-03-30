package oauthbootstrap_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
)

func TestBootstrapPersistConfidentialClientStoresRefreshStateOnly(t *testing.T) {
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
		if got := values.Get("client_secret"); got != "secret-123" {
			t.Fatalf("client_secret = %q, want secret-123", got)
		}
		if got := values.Get("code_verifier"); got != "" {
			t.Fatalf("code_verifier = %q, want omitted for confidential client", got)
		}
	})
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "")
		},
	})

	result, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:      singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID:     "client-123",
		ClientSecret: "secret-123",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.UsedPKCE {
		t.Fatal("UsedPKCE = true, want false for confidential client")
	}
	if diff := cmp.Diff([]string{
		"github.com/example/github-issues/github_oauth/client_id",
		"github.com/example/github-issues/github_oauth/client_secret",
		"github.com/example/github-issues/github_oauth/refresh_token",
	}, result.PersistedKeys); diff != "" {
		t.Fatalf("PersistedKeys mismatch (-want +got):\n%s", diff)
	}
	assertStoredKeys(t, store, map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-123",
		"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-xyz",
	})
	assertSecretAbsent(t, store, "github.com/example/github-issues/github_oauth/access_token")
}

func TestBootstrapTenantNamespaceWritesUnderTenantFamily(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "")
		},
	})

	result, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		Tenant:   "acme",
		ClientID: "client-tenant",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.SecretNamespace; got != "github.com/example/github-issues/tenant/acme/github_oauth" {
		t.Fatalf("SecretNamespace = %q, want tenant namespace", got)
	}
	assertStoredKeys(t, store, map[string]string{
		"github.com/example/github-issues/tenant/acme/github_oauth/client_id":     "client-tenant",
		"github.com/example/github-issues/tenant/acme/github_oauth/refresh_token": "refresh-xyz",
	})
	assertSecretAbsent(t, store, "github.com/example/github-issues/github_oauth/refresh_token")
}

func TestBootstrapOverwriteReplacesExistingRefreshState(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "stale-client",
		"github.com/example/github-issues/github_oauth/client_secret": "stale-secret",
		"github.com/example/github-issues/github_oauth/refresh_token": "stale-refresh",
	})
	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "")
		},
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:      singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID:     "client-rotated",
		ClientSecret: "secret-rotated",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertStoredKeys(t, store, map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "client-rotated",
		"github.com/example/github-issues/github_oauth/client_secret": "secret-rotated",
		"github.com/example/github-issues/github_oauth/refresh_token": "refresh-xyz",
	})
}

func TestBootstrapPersistRollbackLeavesPriorDurableStateIntact(t *testing.T) {
	t.Parallel()

	base := testutil.NewTestSecretStore()
	base.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "prior-client",
		"github.com/example/github-issues/github_oauth/refresh_token": "prior-refresh",
	})
	store := &failingStore{SecretStore: base, failKey: "github.com/example/github-issues/github_oauth/refresh_token", failErr: errors.New("disk full")}
	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "")
		},
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		ClientID: "client-new",
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if !strings.Contains(err.Error(), "during secret persistence") || !strings.Contains(err.Error(), store.failKey) {
		t.Fatalf("error = %v, want failing family key in persistence stage", err)
	}
	assertStoredKeys(t, base, map[string]string{
		"github.com/example/github-issues/github_oauth/client_id":     "prior-client",
		"github.com/example/github-issues/github_oauth/refresh_token": "prior-refresh",
	})
}

func TestBootstrapPersistRejectsMalformedTenant(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	provider, tokenServer := newOAuthTestProvider(t, nil)
	defer tokenServer.Close()
	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: tokenServer.Client(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			return completeCallback(t, authURL, "auth-code", "")
		},
	})

	_, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "github_oauth", []string{"repo"}),
		Tenant:   "bad/name",
		ClientID: "client-123",
	})
	if err == nil {
		t.Fatal("expected malformed tenant error")
	}
	if !strings.Contains(err.Error(), "during secret persistence") || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("error = %v, want tenant namespace failure", err)
	}
}

type failingStore struct {
	secrets.SecretStore
	failKey string
	failErr error
}

func (s *failingStore) Set(ctx context.Context, key string, value []byte) error {
	if key == s.failKey {
		return s.failErr
	}
	return s.SecretStore.Set(ctx, key, value)
}
