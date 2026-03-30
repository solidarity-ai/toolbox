package invoke_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestGoFetchAuthEmulateRouteReturns401WithoutInjectedAuth(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-unauth")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())
	rewriteGithubIssuesCredentialHost(t, workDir, "example.invalid")

	resolved := resolveGithubIssuesToolset(t, workDir, nil)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	assertUnauthenticatedAuthGateError(t, err, wantTitle)
}

func TestGoFetchAuthEmulateRouteReturns401WhenHostDoesNotMatchCredentialRule(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-host-miss")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())
	rewriteGithubIssuesCredentialHost(t, workDir, "example.invalid")

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": srv.Token(),
	})

	resolved := resolveGithubIssuesToolset(t, workDir, secretStore)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	assertUnauthenticatedAuthGateError(t, err, wantTitle)
}

func TestGoFetchAuthMissingTransportCredentialReturnsHelpfulError(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, _ := seedGithubIssueCanary(t, srv, "go-fetch-missing-secret")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	resolved := resolveGithubIssuesToolset(t, workDir, nil)
	_, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	if err == nil {
		t.Fatal("expected missing transport secret error")
	}
	if !strings.Contains(err.Error(), "no secret store") {
		t.Fatalf("error = %v, want missing secret store context", err)
	}
	if !strings.Contains(err.Error(), "github.com/example/github-issues/github_token") {
		t.Fatalf("error = %v, want package-scoped credential key", err)
	}
}

func TestGoFetchAuthEmulateRouteSucceedsWithResolvedTransportAuth(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-auth")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": srv.Token(),
	})

	resolved := resolveGithubIssuesToolset(t, workDir, secretStore)
	result, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var issue struct {
		Number int      `json:"number"`
		Title  string   `json:"title"`
		State  string   `json:"state"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(result), &issue); err != nil {
		t.Fatalf("unmarshal result: %v; raw=%s", err, result)
	}
	if issue.Number != issueNumber {
		t.Fatalf("issue number = %d, want %d", issue.Number, issueNumber)
	}
	if issue.Title != wantTitle {
		t.Fatalf("issue title = %q, want %q", issue.Title, wantTitle)
	}
	if issue.State != "open" {
		t.Fatalf("issue state = %q, want open", issue.State)
	}
	if strings.Contains(result, srv.Token()) {
		t.Fatalf("tool result leaked transport-managed auth token: %s", result)
	}
}

func TestGoFetchAuthMultiProviderOverrideAndNoAuthRuntimeSelection(t *testing.T) {
	t.Parallel()

	var protectedMu sync.Mutex
	protectedRequests := make([]runtimeAuthObservation, 0, 2)
	publicRequests := make([]runtimeAuthObservation, 0, 1)

	protectedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedMu.Lock()
		protectedRequests = append(protectedRequests, runtimeAuthObservation{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			PackageKey:    r.Header.Get("X-Package-Key"),
		})
		protectedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"surface":"protected"}`))
	}))
	defer protectedServer.Close()

	publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedMu.Lock()
		publicRequests = append(publicRequests, runtimeAuthObservation{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			PackageKey:    r.Header.Get("X-Package-Key"),
		})
		protectedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"surface":"public"}`))
	}))
	defer publicServer.Close()

	protectedHost := mustRuntimeTestHost(t, protectedServer.URL)
	publicHost := mustRuntimeTestHost(t, publicServer.URL)

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/mixed-auth/package_token":  "package-bearer",
		"github.com/example/mixed-auth/override_token": "override-bearer",
	})

	resolved := resolveMixedRuntimeAuthToolset(t, secretStore, protectedHost, publicHost)

	inheritResult, err := invoke.Run(resolved, "mixed.inherit", map[string]any{"url": protectedServer.URL + "/inherit"})
	if err != nil {
		t.Fatalf("Run(mixed.inherit): %v", err)
	}
	assertRuntimeToolResult(t, inheritResult, "protected", "package-bearer", "override-bearer")

	overrideResult, err := invoke.Run(resolved, "mixed.override", map[string]any{"url": protectedServer.URL + "/override"})
	if err != nil {
		t.Fatalf("Run(mixed.override): %v", err)
	}
	assertRuntimeToolResult(t, overrideResult, "protected", "package-bearer", "override-bearer")

	noneResult, err := invoke.Run(resolved, "mixed.none", map[string]any{"url": publicServer.URL + "/public"})
	if err != nil {
		t.Fatalf("Run(mixed.none): %v", err)
	}
	assertRuntimeToolResult(t, noneResult, "public", "package-bearer", "override-bearer")

	protectedMu.Lock()
	defer protectedMu.Unlock()
	if len(protectedRequests) != 2 {
		t.Fatalf("protected request count = %d, want 2", len(protectedRequests))
	}
	if protectedRequests[0].Path != "/inherit" {
		t.Fatalf("inherit path = %q, want /inherit", protectedRequests[0].Path)
	}
	if protectedRequests[0].Authorization != "Bearer package-bearer" {
		t.Fatalf("inherit authorization = %q, want Bearer package-bearer", protectedRequests[0].Authorization)
	}
	if protectedRequests[0].PackageKey != "" {
		t.Fatalf("inherit X-Package-Key = %q, want empty", protectedRequests[0].PackageKey)
	}
	if protectedRequests[1].Path != "/override" {
		t.Fatalf("override path = %q, want /override", protectedRequests[1].Path)
	}
	if protectedRequests[1].Authorization != "Bearer override-bearer" {
		t.Fatalf("override authorization = %q, want Bearer override-bearer", protectedRequests[1].Authorization)
	}
	if protectedRequests[1].PackageKey != "" {
		t.Fatalf("override X-Package-Key = %q, want empty to prove replace-not-merge", protectedRequests[1].PackageKey)
	}
	if len(publicRequests) != 1 {
		t.Fatalf("public request count = %d, want 1", len(publicRequests))
	}
	if publicRequests[0].Path != "/public" {
		t.Fatalf("public path = %q, want /public", publicRequests[0].Path)
	}
	if publicRequests[0].Authorization != "" {
		t.Fatalf("public authorization = %q, want empty", publicRequests[0].Authorization)
	}
	if publicRequests[0].PackageKey != "" {
		t.Fatalf("public X-Package-Key = %q, want empty", publicRequests[0].PackageKey)
	}
}

type runtimeAuthObservation struct {
	Path          string
	Authorization string
	PackageKey    string
}

func resolveMixedRuntimeAuthToolset(t testing.TB, secretStore secrets.SecretStore, protectedHost, publicHost string) toolset.ResolvedToolset {
	t.Helper()

	packageRoot := t.TempDir()
	entryRelPath := "tools/runtime-auth.fetch.ts"
	if err := os.MkdirAll(packageRoot+"/tools", 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	toolSource := `export default async function tool(args: { url: string }, _ctx: Record<string, unknown>): Promise<{status:number, body:string}> {
  const resp = await fetch(args.url, { headers: { Accept: "application/json" } });
  const body = await resp.text();
  if (resp.status !== 200) {
    throw new Error(` + "`" + `upstream ${resp.status}: ${body}` + "`" + `);
  }
  return { status: resp.status, body };
}
`
	if err := os.WriteFile(packageRoot+"/"+entryRelPath, []byte(toolSource), 0o644); err != nil {
		t.Fatalf("write runtime auth tool: %v", err)
	}

	basePkg := &tooldef.Package{
		Module:  "github.com/example/mixed-auth",
		Name:    "mixed-auth",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name: "package_token",
			Type: tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{protectedHost},
				Method: "bearer_header",
			},
		}},
	}

	toolFS := os.DirFS(packageRoot)
	tools := []tooldef.ResolvedTool{
		{
			Name:                 "mixed.inherit",
			Description:          "Uses inherited package credentials",
			Package:              basePkg,
			AllowedHosts:         []string{protectedHost},
			EffectiveCredentials: basePkg.Credentials,
			TS:                   &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
		{
			Name:         "mixed.override",
			Description:  "Uses replacement bearer credential",
			Package:      basePkg,
			AllowedHosts: []string{protectedHost},
			EffectiveCredentials: []tooldef.PackageCredential{{
				Name: "override_token",
				Type: tooldef.CredentialTypeBearer,
				Inject: tooldef.CredentialInject{
					Hosts:  []string{protectedHost},
					Method: "bearer_header",
				},
			}},
			TS: &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
		{
			Name:         "mixed.none",
			Description:  "Explicit no-auth public tool",
			Package:      basePkg,
			AllowedHosts: []string{publicHost},
			TS:           &tooldef.TSToolDef{Entry: entryRelPath, Files: toolFS, PackageRoot: packageRoot},
		},
	}

	for i := range tools {
		tools[i].SetParamsSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
			"required": []any{"url"},
		})
	}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}

func assertRuntimeToolResult(t testing.TB, raw, wantSurface string, forbiddenSubstrings ...string) {
	t.Helper()
	for _, forbidden := range forbiddenSubstrings {
		if forbidden != "" && strings.Contains(raw, forbidden) {
			t.Fatalf("tool result leaked runtime credential material %q: %s", forbidden, raw)
		}
	}
	var payload struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal runtime tool result: %v; raw=%s", err, raw)
	}
	if payload.Status != http.StatusOK {
		t.Fatalf("tool status = %d, want %d", payload.Status, http.StatusOK)
	}
	if !strings.Contains(payload.Body, `"surface":"`+wantSurface+`"`) {
		t.Fatalf("tool body = %q, want surface %q", payload.Body, wantSurface)
	}
}

func mustRuntimeTestHost(t testing.TB, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	return parsed.Hostname()
}

func resolveGithubIssuesToolset(t testing.TB, workDir string, secretStore secrets.SecretStore) toolset.ResolvedToolset {
	t.Helper()

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}

	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	return resolved
}

func seedGithubIssueCanary(t testing.TB, srv *emulatetest.Server, prefix string) (owner, repo string, issueNumber int, title string) {
	t.Helper()

	seed := srv.Seed()
	repo = fmt.Sprintf("%s-%s", prefix, strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))
	owner = "admin"

	createdRepo, err := seed.CreateRepo(owner, repo)
	if err != nil && !strings.Contains(err.Error(), "Repository already exists") {
		t.Fatalf("CreateRepo(%s/%s): %v", owner, repo, err)
	}
	if createdRepo != nil && createdRepo.FullName != "" {
		parts := strings.SplitN(createdRepo.FullName, "/", 2)
		if len(parts) == 2 {
			owner = parts[0]
			repo = parts[1]
		}
	}

	title = fmt.Sprintf("auth canary %s", prefix)
	issue, err := seed.CreateIssue(owner, repo, title)
	if err != nil {
		t.Fatalf("CreateIssue(%s/%s): %v", owner, repo, err)
	}
	return owner, repo, issue.Number, title
}

func rewriteGithubIssuesCredentialHost(t testing.TB, workDir, host string) {
	t.Helper()

	manifestPath, manifest := loadGithubIssuesManifestForRewrite(t, workDir)
	credentials, ok := manifest["credentials"].([]any)
	if !ok || len(credentials) != 1 {
		t.Fatalf("credentials malformed in %s: %#v", manifestPath, manifest["credentials"])
	}
	credential, ok := credentials[0].(map[string]any)
	if !ok {
		t.Fatalf("credential malformed in %s: %#v", manifestPath, credentials[0])
	}
	inject, ok := credential["inject"].(map[string]any)
	if !ok {
		t.Fatalf("inject config malformed in %s: %#v", manifestPath, credential["inject"])
	}
	inject["hosts"] = []string{host}
	writeGithubIssuesManifestRewrite(t, manifestPath, manifest)
}

func loadGithubIssuesManifestForRewrite(t testing.TB, workDir string) (string, map[string]any) {
	t.Helper()

	manifestPath := workDir + "/toolbox.devpkg.json"
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest for host rewrite: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest for host rewrite: %v", err)
	}
	return manifestPath, manifest
}

func writeGithubIssuesManifestRewrite(t testing.TB, manifestPath string, manifest map[string]any) {
	t.Helper()

	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest for host rewrite: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write manifest for host rewrite: %v", err)
	}
}

func assertUnauthenticatedAuthGateError(t testing.TB, err error, wantTitle string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected auth-gated emulate route to reject unauthenticated fetch")
	}
	if !strings.Contains(err.Error(), "GitHub API 401") {
		t.Fatalf("error = %v, want GitHub API 401", err)
	}
	if !strings.Contains(err.Error(), "Authorization: Bearer <redacted>") {
		t.Fatalf("error = %v, want auth-gate diagnostic", err)
	}
	if strings.Contains(err.Error(), wantTitle) {
		t.Fatalf("error leaked upstream issue payload instead of failing at auth gate: %v", err)
	}
}
