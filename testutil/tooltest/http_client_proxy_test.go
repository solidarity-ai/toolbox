package tooltest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestPrepareOAuthHTTPClientFixtureRewriteAndValidation(t *testing.T) {
	provider := tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
		AuthURL:  "https://127.0.0.1:9443/oauth/authorize",
		TokenURL: "https://127.0.0.1:9443/oauth/token",
	}}

	t.Run("rewrites fixture to single oauth tswasm contract", func(t *testing.T) {
		dir := CopyHTTPClientFixture(t)
		if err := rewriteOAuthHTTPClientFixture(dir, "https://127.0.0.1:8443/proxy/ok", provider); err != nil {
			t.Fatalf("rewriteOAuthHTTPClientFixture(): %v", err)
		}

		manifest := readHTTPClientTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"))
		if got := manifest["runtime"]; got != string(HTTPClientExpectedPackageRuntime) {
			t.Fatalf("runtime = %#v, want %q", got, HTTPClientExpectedPackageRuntime)
		}
		executables, ok := manifest["executables"].(map[string]any)
		if !ok {
			t.Fatalf("executables = %#v, want map", manifest["executables"])
		}
		if got := executables[HTTPClientExecutableName]; got != "dist/http-client.wasm" {
			t.Fatalf("executables[%q] = %#v, want dist/http-client.wasm", HTTPClientExecutableName, got)
		}
		if got, ok := manifest["allowed_hosts"].([]any); !ok || len(got) != 1 || got[0] != "127.0.0.1" {
			t.Fatalf("allowed_hosts = %#v, want [127.0.0.1]", manifest["allowed_hosts"])
		}
		credentials, ok := manifest["credentials"].([]any)
		if !ok || len(credentials) != 1 {
			t.Fatalf("credentials = %#v, want single oauth credential", manifest["credentials"])
		}
		credential := credentials[0].(map[string]any)
		if credential["name"] != HTTPClientOAuthCredentialName {
			t.Fatalf("credential.name = %#v, want %q", credential["name"], HTTPClientOAuthCredentialName)
		}
		if credential["type"] != string(tooldef.CredentialTypeOAuth2) {
			t.Fatalf("credential.type = %#v, want oauth2", credential["type"])
		}
		inject := credential["inject"].(map[string]any)
		if got, ok := inject["hosts"].([]any); !ok || len(got) != 1 || got[0] != "127.0.0.1" {
			t.Fatalf("inject.hosts = %#v, want [127.0.0.1]", inject["hosts"])
		}
		if inject["method"] != "bearer_header" {
			t.Fatalf("inject.method = %#v, want bearer_header", inject["method"])
		}
		if _, ok := inject["pathPrefix"]; ok {
			t.Fatalf("inject.pathPrefix = %#v, want omitted for full-host oauth coverage", inject["pathPrefix"])
		}
		providerValue := credential["provider"].(map[string]any)
		if providerValue["auth_url"] != "https://127.0.0.1:9443/oauth/authorize" {
			t.Fatalf("provider.auth_url = %#v, want rewritten auth url", providerValue["auth_url"])
		}
		if providerValue["token_url"] != "https://127.0.0.1:9443/oauth/token" {
			t.Fatalf("provider.token_url = %#v, want rewritten token url", providerValue["token_url"])
		}

		resolved, err := HTTPClientBuilderFromDir(t, dir).Resolve(toolset.Config{})
		if err != nil {
			t.Fatalf("Resolve(toolset.Config{}): %v", err)
		}
		if len(resolved.Tools()) != 1 {
			t.Fatalf("resolved tool count = %d, want 1", len(resolved.Tools()))
		}
		tool := resolved.Tools()[0]
		if tool.TSWasm == nil {
			t.Fatal("resolved tool lost TSWasm runtime")
		}
		if got := tool.Package.Runtime; got != HTTPClientExpectedPackageRuntime {
			t.Fatalf("resolved package runtime = %q, want %q", got, HTTPClientExpectedPackageRuntime)
		}
		if got := tool.TSWasm.Executables[HTTPClientExecutableName]; got != "dist/http-client.wasm" {
			t.Fatalf("tswasm executable mapping = %q, want dist/http-client.wasm", got)
		}
		policy, ok := resolved.ToolTransportPolicy(HTTPClientToolName)
		if !ok || policy == nil {
			t.Fatal("expected runtime transport policy for oauth fixture")
		}
		if got := strings.Join(policy.AllowedHosts(), ","); got != "127.0.0.1" {
			t.Fatalf("allowed hosts = %v, want [127.0.0.1]", policy.AllowedHosts())
		}
		rules := policy.Rules()
		if len(rules) != 1 {
			t.Fatalf("transport rule count = %d, want 1", len(rules))
		}
		if got := rules[0].OAuth2SecretFamily; got != HTTPClientOAuthSecretFamily(t) {
			t.Fatalf("oauth2 secret family = %q, want %q", got, HTTPClientOAuthSecretFamily(t))
		}
		AssertHTTPClientAgentViewHidden(t, resolved)
	})

	t.Run("rejects malformed base url", func(t *testing.T) {
		dir := CopyHTTPClientFixture(t)
		err := rewriteOAuthHTTPClientFixture(dir, "://bad", provider)
		if err == nil || !strings.Contains(err.Error(), "base URL") {
			t.Fatalf("rewriteOAuthHTTPClientFixture() error = %v, want base URL validation", err)
		}
	})

	t.Run("rejects wrong runtime before runtime execution can drift", func(t *testing.T) {
		dir := t.TempDir()
		writeHTTPClientFixtureTestFiles(t, dir, map[string]any{
			"module":      HTTPClientModule,
			"name":        "http-client",
			"runtime":     "typescript-sandbox",
			"executables": map[string]any{HTTPClientExecutableName: "dist/http-client.wasm"},
			"tools":       []map[string]any{{"entry_ts": "tools/http-client.fetch.ts", "effect": "readOnly", "idempotent": true}},
		})
		err := rewriteOAuthHTTPClientFixture(dir, "https://127.0.0.1:8443/proxy/ok", provider)
		if err == nil || !strings.Contains(err.Error(), "runtime") {
			t.Fatalf("rewriteOAuthHTTPClientFixture() error = %v, want runtime validation", err)
		}
	})

	t.Run("rejects malformed provider endpoint url", func(t *testing.T) {
		dir := CopyHTTPClientFixture(t)
		badProvider := tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
			AuthURL:  "http://127.0.0.1:9443/oauth/authorize",
			TokenURL: "https://127.0.0.1:9443/oauth/token",
		}}
		err := rewriteOAuthHTTPClientFixture(dir, "https://127.0.0.1:8443/proxy/ok", badProvider)
		if err == nil || !strings.Contains(err.Error(), "provider rewrite invalid") {
			t.Fatalf("rewriteOAuthHTTPClientFixture() error = %v, want provider validation", err)
		}
	})
}

func TestPrepareOAuthHTTPClientFixtureDurableStateAssertions(t *testing.T) {
	harness := NewGoogleAuthHarness(t)
	ctx := t.Context()

	if err := harness.Store.Set(ctx, HTTPClientOAuthSecretKey(t, "client_id"), []byte("client-proxy")); err != nil {
		t.Fatalf("Set client_id: %v", err)
	}
	if err := harness.Store.Set(ctx, HTTPClientOAuthSecretKey(t, "refresh_token"), []byte("google_refresh_proxy")); err != nil {
		t.Fatalf("Set refresh_token: %v", err)
	}
	keys := AssertHTTPClientDurableOAuthState(t, harness.Store, "client-proxy", "")
	if diff := strings.Join(keys, ","); strings.Contains(diff, HTTPClientSecretKey(t, "bearer_token")) {
		t.Fatalf("durable oauth keys unexpectedly included legacy static secret key: %v", keys)
	}
	assertSecretStoreMissing(t, harness.Store, HTTPClientOAuthSecretKey(t, "access_token"))
}

func writeHTTPClientFixtureTestFiles(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "http-client.fetch.ts"), []byte(`export default async function tool(url: string) { return url; }
`), 0o644); err != nil {
		t.Fatalf("write tool fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "http-client.wasm"), []byte("placeholder wasm"), 0o644); err != nil {
		t.Fatalf("write wasm fixture: %v", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toolbox.devpkg.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func readHTTPClientTestManifest(t *testing.T, path string) map[string]any {
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
