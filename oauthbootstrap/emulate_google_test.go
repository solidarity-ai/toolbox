package oauthbootstrap_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/secrets"
)

func TestGoogleEmulateBootstrapConfidentialOverwriteAndNoAccessTokenPersistence(t *testing.T) {
	srv := emulatetest.StartGoogle(t)
	provider, err := srv.GoogleProviderRef(context.Background())
	if err != nil {
		t.Fatalf("GoogleProviderRef(): %v", err)
	}
	store := newLocalSecretStoreForBootstrapTest(t)
	ctx := context.Background()

	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:           store,
		HTTPClient:      srv.SecureClient(),
		OpenBrowser:     func(ctx context.Context, authURL string) error { return srv.CompleteGoogleAuthorization(ctx, authURL) },
		CallbackTimeout: 5 * time.Second,
		ExchangeTimeout: 5 * time.Second,
	})

	first, err := bootstrap.Run(ctx, oauthbootstrap.Request{
		Package:      singleCredentialPackage(provider, "workspace", []string{"openid", "email"}),
		ClientID:     "client-confidential-one",
		ClientSecret: "secret-confidential-one",
	})
	if err != nil {
		t.Fatalf("first Run(): %v", err)
	}
	if first.UsedPKCE {
		t.Fatal("first UsedPKCE = true, want false for confidential client")
	}
	if diff := cmp.Diff([]string{
		"github.com/example/github-issues/workspace/client_id",
		"github.com/example/github-issues/workspace/client_secret",
		"github.com/example/github-issues/workspace/refresh_token",
	}, first.PersistedKeys); diff != "" {
		t.Fatalf("first PersistedKeys mismatch (-want +got):\n%s", diff)
	}
	if got := first.SecretNamespace; got != "github.com/example/github-issues/workspace" {
		t.Fatalf("first SecretNamespace = %q, want package namespace", got)
	}
	firstRefresh := mustLocalSecret(t, store, "github.com/example/github-issues/workspace/refresh_token")
	assertLocalStoreKeys(t, store, "github.com/example/github-issues/workspace", []string{
		"github.com/example/github-issues/workspace/client_id",
		"github.com/example/github-issues/workspace/client_secret",
		"github.com/example/github-issues/workspace/refresh_token",
	})
	assertLocalSecrets(t, store, map[string]string{
		"github.com/example/github-issues/workspace/client_id":     "client-confidential-one",
		"github.com/example/github-issues/workspace/client_secret": "secret-confidential-one",
	})
	assertLocalSecretAbsent(t, store, "github.com/example/github-issues/workspace/access_token")

	second, err := bootstrap.Run(ctx, oauthbootstrap.Request{
		Package:      singleCredentialPackage(provider, "workspace", []string{"openid", "email"}),
		ClientID:     "client-confidential-two",
		ClientSecret: "secret-confidential-two",
	})
	if err != nil {
		t.Fatalf("second Run(): %v", err)
	}
	if second.UsedPKCE {
		t.Fatal("second UsedPKCE = true, want false for confidential client")
	}
	secondRefresh := mustLocalSecret(t, store, "github.com/example/github-issues/workspace/refresh_token")
	if secondRefresh == firstRefresh {
		t.Fatalf("refresh_token was not overwritten: first=%q second=%q", firstRefresh, secondRefresh)
	}
	assertLocalSecrets(t, store, map[string]string{
		"github.com/example/github-issues/workspace/client_id":     "client-confidential-two",
		"github.com/example/github-issues/workspace/client_secret": "secret-confidential-two",
		"github.com/example/github-issues/workspace/refresh_token": secondRefresh,
	})
	assertLocalSecretAbsent(t, store, "github.com/example/github-issues/workspace/access_token")
}

func TestGoogleEmulateBootstrapTenantNamespaceUsesPKCEByDefault(t *testing.T) {
	srv := emulatetest.StartGoogle(t)
	provider, err := srv.GoogleProviderRef(context.Background())
	if err != nil {
		t.Fatalf("GoogleProviderRef(): %v", err)
	}
	store := newLocalSecretStoreForBootstrapTest(t)

	var openedAuthURL string
	bootstrap := oauthbootstrap.New(oauthbootstrap.Options{
		Store:      store,
		HTTPClient: srv.SecureClient(),
		OpenBrowser: func(ctx context.Context, authURL string) error {
			openedAuthURL = authURL
			return srv.CompleteGoogleAuthorization(ctx, authURL)
		},
		CallbackTimeout: 5 * time.Second,
		ExchangeTimeout: 5 * time.Second,
	})

	result, err := bootstrap.Run(context.Background(), oauthbootstrap.Request{
		Package:  singleCredentialPackage(provider, "workspace", []string{"openid", "email"}),
		Tenant:   "acme",
		ClientID: "client-public",
	})
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if !result.UsedPKCE {
		t.Fatal("UsedPKCE = false, want true for public client")
	}
	if got := result.SecretNamespace; got != "github.com/example/github-issues/tenant/acme/workspace" {
		t.Fatalf("SecretNamespace = %q, want tenant namespace", got)
	}
	if !strings.Contains(openedAuthURL, "code_challenge=") || !strings.Contains(openedAuthURL, "code_challenge_method=S256") {
		t.Fatalf("AuthorizationURL = %q, want PKCE challenge parameters", openedAuthURL)
	}
	assertLocalStoreKeys(t, store, "github.com/example/github-issues/tenant/acme/workspace", []string{
		"github.com/example/github-issues/tenant/acme/workspace/client_id",
		"github.com/example/github-issues/tenant/acme/workspace/refresh_token",
	})
	assertLocalSecrets(t, store, map[string]string{
		"github.com/example/github-issues/tenant/acme/workspace/client_id":     "client-public",
		"github.com/example/github-issues/tenant/acme/workspace/refresh_token": mustLocalSecret(t, store, "github.com/example/github-issues/tenant/acme/workspace/refresh_token"),
	})
	assertLocalSecretAbsent(t, store, "github.com/example/github-issues/tenant/acme/workspace/client_secret")
	assertLocalSecretAbsent(t, store, "github.com/example/github-issues/tenant/acme/workspace/access_token")
	assertLocalSecretAbsent(t, store, "github.com/example/github-issues/workspace/refresh_token")
}

func newLocalSecretStoreForBootstrapTest(t *testing.T) *secrets.LocalSecretStore {
	t.Helper()
	identityDir := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity(): %v", err)
	}
	identityPath := filepath.Join(identityDir, "keys.txt")
	if err := os.WriteFile(identityPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", identityPath, err)
	}
	return secrets.NewLocalSecretStore(filepath.Join(t.TempDir(), "secrets.age"), identityPath)
}

func assertLocalStoreKeys(t *testing.T, store *secrets.LocalSecretStore, prefix string, want []string) {
	t.Helper()
	got, err := store.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("List(%q): %v", prefix, err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("List(%q) mismatch (-want +got):\n%s", prefix, diff)
	}
}

func assertLocalSecrets(t *testing.T, store *secrets.LocalSecretStore, want map[string]string) {
	t.Helper()
	for key, expected := range want {
		if got := mustLocalSecret(t, store, key); got != expected {
			t.Fatalf("Get(%q) = %q, want %q", key, got, expected)
		}
	}
}

func mustLocalSecret(t *testing.T, store *secrets.LocalSecretStore, key string) string {
	t.Helper()
	value, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return string(value)
}

func assertLocalSecretAbsent(t *testing.T, store *secrets.LocalSecretStore, key string) {
	t.Helper()
	_, err := store.Get(context.Background(), key)
	if err == nil {
		t.Fatalf("Get(%q) unexpectedly succeeded", key)
	}
	if err != secrets.ErrNotFound {
		t.Fatalf("Get(%q) error = %v, want ErrNotFound", key, err)
	}
}
