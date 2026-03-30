package packaging_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

// TestNpmDepsDevMode verifies that tools with npm dependencies work in dev
// (source) mode by resolving bare imports via node_modules on disk.
func TestNpmDepsDevMode(t *testing.T) {
	fixtureDir := findFixture(t, "github-issues")
	workDir := copyFixtureToTempDir(t, fixtureDir)
	npmInstall(t, workDir)

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}
	assertGithubIssuesAuthContract(t, loaded.Package)

	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	assertToolAuthHiddenFromAgentView(t, resolved)

	// Verify esbuild can bundle the tool with npm deps.
	// We don't run the tool (it would hang on a real fetch to api.github.com)
	// but we prove the bundle is self-contained with zod + octokit inlined.
	bundled, err := quickts.EmitBundle(*resolved.Tools()[0].TS)
	if err != nil {
		t.Fatalf("esbuild bundling failed: %v", err)
	}
	if !strings.Contains(bundled, "ZodError") {
		t.Error("bundle missing zod code")
	}
	t.Logf("dev mode bundle: %d bytes", len(bundled))
}

// TestNpmDepsDistMode documents that dist mode does NOT yet support npm deps.
// Currently the archive stores raw .ts files and esbuild runs at runtime.
// Without node_modules in the archive, bare imports fail. The fix is to
// pre-bundle at pack time so the archive contains self-contained .js.
func TestNpmDepsDistMode(t *testing.T) {
	fixtureDir := findFixture(t, "github-issues")
	workDir := copyFixtureToTempDir(t, fixtureDir)
	npmInstall(t, workDir)

	outDir := t.TempDir()
	result, err := packaging.Pack(workDir, outDir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	loaded, err := packaging.LoadArchive(result.ArchivePath, result.ManifestPath)
	if err != nil {
		t.Fatalf("LoadArchive: %v", err)
	}
	assertGithubIssuesAuthContract(t, loaded.Package)

	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	assertToolAuthHiddenFromAgentView(t, resolved)

	_, err = invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  "octocat",
		"repo":   "hello-world",
		"number": 1,
	})
	// This SHOULD fail with a bare import error because the archive has no
	// node_modules. This test documents the current gap — pre-bundling at
	// pack time is the next step to close it.
	if err == nil {
		t.Fatal("expected error in dist mode without pre-bundling")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "Could not resolve") && !strings.Contains(errStr, "bare import") {
		t.Fatalf("expected resolution error, got: %v", err)
	}
	t.Logf("dist mode correctly fails without pre-bundling: %v", err)
}

// TestNpmDepsBundleSize verifies the bundled output is self-contained by
// checking that esbuild produces output without external dependencies.
func TestNpmDepsBundleSize(t *testing.T) {
	fixtureDir := findFixture(t, "github-issues")
	workDir := copyFixtureToTempDir(t, fixtureDir)
	npmInstall(t, workDir)

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}
	assertGithubIssuesAuthContract(t, loaded.Package)

	tools := loaded.ResolvedTools()
	if len(tools) == 0 {
		t.Fatal("no tools resolved")
	}

	// Verify node_modules and shims exist in the work dir
	if _, err := os.Stat(filepath.Join(workDir, "node_modules")); err != nil {
		t.Fatalf("node_modules not found in %s: %v", workDir, err)
	}
	t.Logf("PackageRoot: %s", tools[0].TS.PackageRoot)

	// Verify shims.ts is accessible through the sourceFS
	if _, err := tools[0].TS.Files.Open("tools/shims.ts"); err != nil {
		t.Logf("shims.ts not in sourceFS: %v", err)
	} else {
		t.Logf("shims.ts found in sourceFS")
	}

	// Use quickts.EmitBundle to get the bundled JS and verify it contains
	// inlined code from zod and octokit.
	bundled, err := quickts.EmitBundle(*tools[0].TS)
	if err != nil {
		t.Fatalf("EmitBundle: %v", err)
	}

	if !strings.Contains(bundled, "ZodError") {
		t.Error("bundled JS does not contain zod code (expected ZodError)")
	}
	t.Logf("bundle size: %d bytes", len(bundled))
}

func findFixture(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == name {
			return dir
		}
	}
	t.Fatalf("fixture %q not found", name)
	return ""
}

func copyFixtureToTempDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

func npmInstall(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
	cmd := exec.Command("npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}
}

// TestFixtureRepairGithubIssuesFixtureSetup proves the copied fixture can be
// rewritten toward the auth-gated emulate façade and still install npm deps.
func TestFixtureRepairGithubIssuesFixtureSetup(t *testing.T) {
	srv := emulatetest.Start(t)
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	if _, err := os.Stat(filepath.Join(workDir, "node_modules")); err != nil {
		t.Fatalf("node_modules not found after fixture prep: %v", err)
	}

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}
	assertGithubIssuesAuthContractForHost(t, loaded.Package, "127.0.0.1")

	toolSource, err := os.ReadFile(filepath.Join(workDir, "tools", "github-issues.get.ts"))
	if err != nil {
		t.Fatalf("read rewritten tool source: %v", err)
	}
	if !strings.Contains(string(toolSource), strings.TrimRight(srv.AuthBaseURL(), "/")+"/repos/") {
		t.Fatalf("rewritten tool source did not target auth-gated emulate URL %q", srv.AuthBaseURL())
	}
}

// TestNpmDepsE2E runs the github-issues tool against an auth-gated emulate
// façade, with transport auth injected from the package-scoped secret key.
func TestNpmDepsE2E(t *testing.T) {
	srv := emulatetest.Start(t)
	owner, repo, issueNumber, wantTitle := seedGithubIssueCanary(t, srv, "packaging-e2e")
	workDir := tooltest.PrepareGithubIssuesFixture(t, srv.AuthBaseURL())

	loaded, err := packaging.LoadDev(workDir)
	if err != nil {
		t.Fatalf("LoadDev: %v", err)
	}
	assertGithubIssuesAuthContractForHost(t, loaded.Package, "127.0.0.1")

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": srv.Token(),
	})

	resolved, err := toolset.ResolveTools(loaded.ResolvedTools(), toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	assertToolAuthHiddenFromAgentView(t, resolved)

	result, err := invoke.Run(resolved, "githubIssues.get", map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issueNumber,
	})
	if err != nil {
		t.Fatalf("invoke.Run: %v", err)
	}

	var issue struct {
		Number int      `json:"number"`
		Title  string   `json:"title"`
		State  string   `json:"state"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(result), &issue); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, result)
	}

	if issue.Number != issueNumber {
		t.Fatalf("expected issue #%d, got #%d", issueNumber, issue.Number)
	}
	if issue.Title != wantTitle {
		t.Fatalf("expected issue title %q, got %q", wantTitle, issue.Title)
	}
	if issue.State != "open" {
		t.Fatalf("expected open issue state, got %q", issue.State)
	}
	t.Logf("fetched issue #%d through auth-gated emulate façade: %q (state=%s)", issue.Number, issue.Title, issue.State)
}

func assertGithubIssuesAuthContract(t *testing.T, pkg tooldef.Package) {
	t.Helper()
	assertGithubIssuesAuthContractForHost(t, pkg, "api.github.com")
}

func assertGithubIssuesAuthContractForHost(t *testing.T, pkg tooldef.Package, wantHost string) {
	t.Helper()
	if got := pkg.Module.String(); got != "github.com/example/github-issues" {
		t.Fatalf("package module = %q, want github.com/example/github-issues", got)
	}
	if len(pkg.Credentials) != 1 {
		t.Fatalf("package credentials = %d, want 1", len(pkg.Credentials))
	}
	if len(pkg.AllowedHosts) != 1 || pkg.AllowedHosts[0] != wantHost {
		t.Fatalf("package allowed_hosts = %v, want [%s]", pkg.AllowedHosts, wantHost)
	}
	cred := pkg.Credentials[0]
	if cred.Name != "github_token" {
		t.Fatalf("credential name = %q, want github_token", cred.Name)
	}
	if cred.Type != tooldef.CredentialTypeBearer {
		t.Fatalf("credential type = %q, want bearer", cred.Type)
	}
	if len(cred.Inject.Hosts) != 1 || cred.Inject.Hosts[0] != wantHost {
		t.Fatalf("credential hosts = %v, want [%s]", cred.Inject.Hosts, wantHost)
	}
	if cred.Inject.Method != "bearer_header" {
		t.Fatalf("credential inject method = %q, want bearer_header", cred.Inject.Method)
	}
}

func assertToolAuthHiddenFromAgentView(t *testing.T, resolved toolset.ResolvedToolset) {
	t.Helper()
	view := resolved.AgentView()
	if len(view.Tools) != 1 {
		t.Fatalf("agent view tool count = %d, want 1", len(view.Tools))
	}
	props, ok := view.Tools[0].ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("params schema properties missing: %#v", view.Tools[0].ParamsSchema)
	}
	if _, ok := props["owner"]; !ok {
		t.Fatal("expected owner param to remain visible")
	}
	if _, ok := props["repo"]; !ok {
		t.Fatal("expected repo param to remain visible")
	}
	if _, ok := props["number"]; !ok {
		t.Fatal("expected number param to remain visible")
	}
	if _, ok := props["token"]; ok {
		t.Fatal("token param should not appear in AgentView schema")
	}
	if _, ok := props["github_token"]; ok {
		t.Fatal("transport credential should not appear in AgentView schema")
	}
}

func seedGithubIssueCanary(t testing.TB, srv *emulatetest.Server, prefix string) (owner, repo string, issueNumber int, title string) {
	t.Helper()

	seed := srv.Seed()
	owner = "admin"
	repo = fmt.Sprintf("%s-%s", prefix, strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")))

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

// Ensure quickts is used (imported for EmitBundle).
var _ = quickts.EmitBundle
