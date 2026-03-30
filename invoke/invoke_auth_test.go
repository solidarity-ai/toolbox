package invoke_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestGoFetchAuthEmulateRouteReturns401WithoutInjectedAuth(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "go-fetch-unauth")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())
	rewriteGithubIssuesAllowedHosts(t, workDir, srv.AuthBaseURL())
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
	rewriteGithubIssuesAllowedHosts(t, workDir, srv.AuthBaseURL())
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
	rewriteGithubIssuesAllowedHosts(t, workDir, srv.AuthBaseURL())

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
	rewriteGithubIssuesAllowedHosts(t, workDir, srv.AuthBaseURL())

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

func rewriteGithubIssuesAllowedHosts(t testing.TB, workDir, baseURL string) {
	t.Helper()

	manifestPath, manifest := loadGithubIssuesManifestForRewrite(t, workDir)
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base url for allowlist rewrite: %v", err)
	}
	manifest["allowed_hosts"] = []string{parsedBaseURL.Hostname()}
	writeGithubIssuesManifestRewrite(t, manifestPath, manifest)
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
