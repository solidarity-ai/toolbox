package tooltest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestPrepareGoogleWorkspaceFixtureRewriteAndValidation(t *testing.T) {
	t.Run("rewrites tool endpoint and package host metadata", func(t *testing.T) {
		dir := t.TempDir()
		writeGoogleWorkspaceFixtureTestFiles(t, dir, map[string]any{
			"name":          "google-workspace",
			"runtime":       "typescript-sandbox",
			"allowed_hosts": []string{"admin.googleapis.com"},
			"credentials": []map[string]any{{
				"name":     "workspace",
				"type":     "oauth2",
				"provider": "google",
				"inject": map[string]any{
					"hosts":  []string{"admin.googleapis.com"},
					"method": "bearer_header",
				},
			}},
		})

		provider := tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
			AuthURL:  "https://127.0.0.1:9443/oauth/authorize",
			TokenURL: "https://127.0.0.1:9443/oauth/token",
		}}
		if err := rewriteGoogleWorkspaceFixtureBaseURL(dir, "https://127.0.0.1:8443", provider); err != nil {
			t.Fatalf("rewriteGoogleWorkspaceFixtureBaseURL(): %v", err)
		}

		toolRaw, err := os.ReadFile(filepath.Join(dir, "tools", "users.list.ts"))
		if err != nil {
			t.Fatalf("read rewritten tool: %v", err)
		}
		if !strings.Contains(string(toolRaw), `const USERS_ENDPOINT = "https://127.0.0.1:8443/admin/directory/v1/users?customer=my_customer&maxResults=1&orderBy=email";`) {
			t.Fatalf("rewritten tool = %s, want local endpoint", string(toolRaw))
		}

		manifest := readGoogleWorkspaceTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"))
		if got, ok := manifest["allowed_hosts"].([]any); !ok || len(got) != 1 || got[0] != "127.0.0.1" {
			t.Fatalf("allowed_hosts = %#v, want [127.0.0.1]", manifest["allowed_hosts"])
		}
		credentials := manifest["credentials"].([]any)
		credential := credentials[0].(map[string]any)
		inject := credential["inject"].(map[string]any)
		if got, ok := inject["hosts"].([]any); !ok || len(got) != 1 || got[0] != "127.0.0.1" {
			t.Fatalf("inject.hosts = %#v, want [127.0.0.1]", inject["hosts"])
		}
		providerValue := credential["provider"].(map[string]any)
		if providerValue["auth_url"] != "https://127.0.0.1:9443/oauth/authorize" {
			t.Fatalf("provider.auth_url = %#v, want rewritten auth url", providerValue["auth_url"])
		}
		if providerValue["token_url"] != "https://127.0.0.1:9443/oauth/token" {
			t.Fatalf("provider.token_url = %#v, want rewritten token url", providerValue["token_url"])
		}
	})

	t.Run("rejects malformed base url", func(t *testing.T) {
		dir := t.TempDir()
		writeGoogleWorkspaceFixtureTestFiles(t, dir, map[string]any{
			"name":    "google-workspace",
			"runtime": "typescript-sandbox",
		})
		err := rewriteGoogleWorkspaceFixtureBaseURL(dir, "://bad", tooldef.OAuth2ProviderRef{})
		if err == nil || !strings.Contains(err.Error(), "base URL") {
			t.Fatalf("rewriteGoogleWorkspaceFixtureBaseURL() error = %v, want base URL validation", err)
		}
	})

	t.Run("rejects malformed provider endpoint url", func(t *testing.T) {
		dir := t.TempDir()
		writeGoogleWorkspaceFixtureTestFiles(t, dir, map[string]any{
			"name":    "google-workspace",
			"runtime": "typescript-sandbox",
			"credentials": []map[string]any{{
				"name":     "workspace",
				"type":     "oauth2",
				"provider": "google",
				"inject": map[string]any{
					"hosts":  []string{"admin.googleapis.com"},
					"method": "bearer_header",
				},
			}},
		})
		provider := tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
			AuthURL:  "http://127.0.0.1:9443/oauth/authorize",
			TokenURL: "https://127.0.0.1:9443/oauth/token",
		}}
		err := rewriteGoogleWorkspaceFixtureBaseURL(dir, "https://127.0.0.1:8443", provider)
		if err == nil || !strings.Contains(err.Error(), "provider rewrite invalid") {
			t.Fatalf("rewriteGoogleWorkspaceFixtureBaseURL() error = %v, want provider validation", err)
		}
	})
}

func TestGoogleAuthHarnessDurableStateAssertions(t *testing.T) {
	harness := NewGoogleAuthHarness(t)
	ctx := context.Background()

	packageRefreshKey := GoogleWorkspaceOAuthSecretKey(t, "refresh_token")
	if err := harness.Store.Set(ctx, GoogleWorkspaceOAuthSecretKey(t, "client_id"), []byte("client-package")); err != nil {
		t.Fatalf("Set package client_id: %v", err)
	}
	if err := harness.Store.Set(ctx, packageRefreshKey, []byte("google_refresh_package")); err != nil {
		t.Fatalf("Set package refresh_token: %v", err)
	}
	packageState := harness.AssertDurableOAuthState(t, "", "client-package", "")
	if packageState.Namespace != GoogleWorkspaceOAuthSecretFamily(t) {
		t.Fatalf("package namespace = %q, want %q", packageState.Namespace, GoogleWorkspaceOAuthSecretFamily(t))
	}

	tenant := "acme"
	if err := harness.Store.Set(ctx, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_id"), []byte("client-tenant")); err != nil {
		t.Fatalf("Set tenant client_id: %v", err)
	}
	if err := harness.Store.Set(ctx, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "refresh_token"), []byte("google_refresh_tenant")); err != nil {
		t.Fatalf("Set tenant refresh_token: %v", err)
	}
	if err := harness.Store.Set(ctx, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_secret"), []byte("tenant-secret")); err != nil {
		t.Fatalf("Set tenant client_secret: %v", err)
	}
	state := harness.AssertDurableOAuthState(t, tenant, "client-tenant", "tenant-secret")
	if state.Namespace != GoogleWorkspaceOAuthSecretFamilyForTenant(t, tenant) {
		t.Fatalf("tenant namespace = %q, want %q", state.Namespace, GoogleWorkspaceOAuthSecretFamilyForTenant(t, tenant))
	}
	if strings.Contains(strings.Join(state.PersistedKeys, ","), packageRefreshKey) {
		t.Fatalf("tenant persisted keys unexpectedly included package-scope key %q: %v", packageRefreshKey, state.PersistedKeys)
	}
	assertLocalStoreMissing(t, harness.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "access_token"))
}

func writeGoogleWorkspaceFixtureTestFiles(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "users.list.ts"), []byte(`const USERS_ENDPOINT = "`+googleWorkspaceDefaultEndpoint+`";
export default async function tool() { return USERS_ENDPOINT; }
`), 0o644); err != nil {
		t.Fatalf("write tool fixture: %v", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toolbox.devpkg.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func readGoogleWorkspaceTestManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

var _ secrets.SecretStore = (*secrets.LocalSecretStore)(nil)
