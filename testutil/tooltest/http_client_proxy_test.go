package tooltest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
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
		AssertPreparedOAuthHTTPClientFixtureContract(t, dir, "https://127.0.0.1:8443/proxy/ok", provider)
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

	t.Run("contract helper fails when executable-backed runtime drifts", func(t *testing.T) {
		dir := CopyHTTPClientFixture(t)
		if err := rewriteOAuthHTTPClientFixture(dir, "https://127.0.0.1:8443/proxy/ok", provider); err != nil {
			t.Fatalf("rewriteOAuthHTTPClientFixture(): %v", err)
		}
		manifest := readHTTPClientTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"))
		manifest["executables"] = map[string]any{}
		writeHTTPClientTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"), manifest)

		assertHTTPClientContractFails(t, func(tb testing.TB) {
			AssertPreparedOAuthHTTPClientFixtureContract(tb, dir, "https://127.0.0.1:8443/proxy/ok", provider)
		}, HTTPClientExecutableName)
	})

	t.Run("contract helper fails when oauth family rewrite drifts", func(t *testing.T) {
		dir := CopyHTTPClientFixture(t)
		if err := rewriteOAuthHTTPClientFixture(dir, "https://127.0.0.1:8443/proxy/ok", provider); err != nil {
			t.Fatalf("rewriteOAuthHTTPClientFixture(): %v", err)
		}
		manifest := readHTTPClientTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"))
		manifest["credentials"] = []map[string]any{{
			"name": HTTPClientOAuthCredentialName,
			"type": string(tooldef.CredentialTypeOAuth2),
			"provider": map[string]any{
				"auth_url":  provider.Endpoints.AuthURL,
				"token_url": provider.Endpoints.TokenURL,
			},
			"scopes": []string{"openid", "email"},
			"inject": map[string]any{
				"hosts":  []string{"example.invalid"},
				"method": "bearer_header",
			},
		}}
		writeHTTPClientTestManifest(t, filepath.Join(dir, "toolbox.devpkg.json"), manifest)

		assertHTTPClientContractFails(t, func(tb testing.TB) {
			AssertPreparedOAuthHTTPClientFixtureContract(tb, dir, "https://127.0.0.1:8443/proxy/ok", provider)
		}, "inject.hosts mismatch")
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

func writeHTTPClientTestManifest(t *testing.T, path string, manifest map[string]any) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func assertHTTPClientContractFails(t *testing.T, fn func(testing.TB), want string) {
	t.Helper()
	inner := &contractFailureTB{TB: t}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(contractFailurePanic); !ok {
				panic(r)
			}
		}
		if !inner.failed {
			t.Fatalf("expected helper to fail with %q", want)
		}
		if !strings.Contains(inner.msg.String(), want) {
			t.Fatalf("helper failure = %q, want substring %q", inner.msg.String(), want)
		}
	}()
	fn(inner)
}

type contractFailureTB struct {
	testing.TB
	failed bool
	msg    strings.Builder
}

type contractFailurePanic struct{}

func (tb *contractFailureTB) Fatal(args ...any) {
	tb.failed = true
	_, _ = tb.msg.WriteString(fmt.Sprint(args...))
	panic(contractFailurePanic{})
}

func (tb *contractFailureTB) Fatalf(format string, args ...any) {
	tb.failed = true
	_, _ = tb.msg.WriteString(fmt.Sprintf(format, args...))
	panic(contractFailurePanic{})
}

var _ secrets.SecretStore = (*secrets.LocalSecretStore)(nil)
