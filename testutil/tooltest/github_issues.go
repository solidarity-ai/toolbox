package tooltest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func githubIssuesFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "github-issues")
}

// GithubIssuesFixtureDir returns the source fixture directory for github-issues.
func GithubIssuesFixtureDir(t testing.TB) string {
	t.Helper()
	return githubIssuesFixtureDir()
}

// CopyGithubIssuesFixture copies the github-issues fixture into a temp dir,
// excluding node_modules so tests can perform a clean npm install.
func CopyGithubIssuesFixture(t testing.TB) string {
	t.Helper()

	src := githubIssuesFixtureDir()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
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
		t.Fatalf("copy github-issues fixture: %v", err)
	}
	return dst
}

// InstallNPMDeps installs npm dependencies for a copied github-issues fixture.
func InstallNPMDeps(t testing.TB, dir string) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
	cmd := exec.Command("npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed in %s: %v\n%s", dir, err, out)
	}
}

// RewriteGithubIssuesFixtureBaseURL rewrites the copied fixture to target the
// provided base URL instead of api.github.com, and updates credential host
// matching plus package allowlist host matching to the base URL hostname so
// transport auth and fetch preflight stay aligned.
func RewriteGithubIssuesFixtureBaseURL(t testing.TB, dir, baseURL string) {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse github-issues base URL %q: %v", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		t.Fatalf("github-issues base URL must include scheme and host, got %q", baseURL)
	}

	manifestPath := filepath.Join(dir, "toolbox.devpkg.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read github-issues manifest: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("decode github-issues manifest: %v", err)
	}

	credentials, ok := manifest["credentials"].([]any)
	if !ok || len(credentials) != 1 {
		t.Fatalf("github-issues manifest credentials malformed: %#v", manifest["credentials"])
	}
	credential, ok := credentials[0].(map[string]any)
	if !ok {
		t.Fatalf("github-issues credential malformed: %#v", credentials[0])
	}
	inject, ok := credential["inject"].(map[string]any)
	if !ok {
		t.Fatalf("github-issues credential inject malformed: %#v", credential["inject"])
	}
	hostname := parsed.Hostname()
	inject["hosts"] = []string{hostname}
	manifest["allowed_hosts"] = []string{hostname}

	updatedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode github-issues manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updatedManifest, '\n'), 0o644); err != nil {
		t.Fatalf("write github-issues manifest: %v", err)
	}

	toolPath := filepath.Join(dir, "tools", "github-issues.get.ts")
	toolRaw, err := os.ReadFile(toolPath)
	if err != nil {
		t.Fatalf("read github-issues tool: %v", err)
	}

	oldURL := "`https://api.github.com/repos/${owner}/${repo}/issues/${number}`"
	newURL := fmt.Sprintf("`%s/repos/${owner}/${repo}/issues/${number}`", strings.TrimRight(baseURL, "/"))
	updatedTool := strings.Replace(string(toolRaw), oldURL, newURL, 1)
	if updatedTool == string(toolRaw) {
		t.Fatalf("github-issues tool did not contain expected base URL %q", oldURL)
	}
	if err := os.WriteFile(toolPath, []byte(updatedTool), 0o644); err != nil {
		t.Fatalf("write github-issues tool: %v", err)
	}
}

// PrepareGithubIssuesFixture copies the fixture, rewrites its fetch target when
// requested, and installs npm dependencies so it is ready for execution.
func PrepareGithubIssuesFixture(t testing.TB, baseURL string) string {
	t.Helper()
	dir := CopyGithubIssuesFixture(t)
	if baseURL != "" {
		RewriteGithubIssuesFixtureBaseURL(t, dir, baseURL)
	}
	InstallNPMDeps(t, dir)
	return dir
}

// GithubIssuesBuilder returns a *toolset.Builder loaded with the github-issues fixture package.
func GithubIssuesBuilder(t testing.TB) *toolset.Builder {
	t.Helper()
	return GithubIssuesBuilderFromDir(t, githubIssuesFixtureDir())
}

// GithubIssuesBuilderFromDir returns a *toolset.Builder loaded from the given
// github-issues fixture directory.
func GithubIssuesBuilderFromDir(t testing.TB, dir string) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(dir); err != nil {
		t.Fatalf("add github-issues package dir: %v", err)
	}
	return builder
}
