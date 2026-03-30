package invoke_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestRunFetchInjectsTransportManagedBearerToken(t *testing.T) {
	t.Parallel()

	secretStore := testutil.NewTestSecretStore()
	secretStore.Seed(map[string][]byte{
		"github.com/example/github-issues/github_token": []byte("secret-token"),
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			http.Error(w, "missing transport auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	resolved := authFetchToolset(t, secretStore, server.URL)
	result, err := invoke.Run(resolved, "github.issues.fetch", map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var payload struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v; raw=%s", err, result)
	}
	if payload.Status != 200 {
		t.Fatalf("status = %d, want 200; raw=%s", payload.Status, result)
	}
	if payload.Body != `{"ok":true}` {
		t.Fatalf("body = %q, want {\"ok\":true}", payload.Body)
	}
}

func TestRunFetchMissingTransportCredentialReturnsHelpfulError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be reached", http.StatusInternalServerError)
	}))
	defer server.Close()

	resolved := authFetchToolset(t, nil, server.URL)
	_, err := invoke.Run(resolved, "github.issues.fetch", map[string]any{"url": server.URL})
	if err == nil {
		t.Fatal("expected missing secret store error")
	}
	if !strings.Contains(err.Error(), "no secret store") {
		t.Fatalf("error = %v, want missing secret store context", err)
	}
	if !strings.Contains(err.Error(), "github.com/example/github-issues/github_token") {
		t.Fatalf("error = %v, want package-scoped credential key", err)
	}
}

func authFetchToolset(t testing.TB, secretStore secrets.SecretStore, serverURL string) toolset.ResolvedToolset {
	t.Helper()

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}

	pkg := tooldef.Package{
		Module:  "github.com/example/github-issues",
		Name:    "github-issues",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name: "github_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{parsedURL.Hostname()},
				Method: "bearer_header",
			},
		}},
	}

	toolFS := fstest.MapFS{
		"tools/fetch.ts": &fstest.MapFile{Data: []byte(`
export default async function tool(params: { url: string }, _ctx: unknown) {
  const resp = await fetch(params.url, {
    headers: {
      "Accept": "application/json"
    }
  });
  return { status: resp.status, body: await resp.text() };
}
`)},
	}

	rt := tooldef.ResolvedTool{
		Name:        "github.issues.fetch",
		Description: "Fetch an authenticated issue endpoint",
		Package:     &pkg,
		TS: &tooldef.TSToolDef{
			Entry: "tools/fetch.ts",
			Files: toolFS,
		},
	}

	resolved, err := toolset.ResolveTools([]tooldef.ResolvedTool{rt}, toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}
