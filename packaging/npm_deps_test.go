package packaging_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
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

	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	// Run the tool — it will fail at runtime (no network / missing QuickJS APIs)
	// but should NOT fail with esbuild "unsupported import" errors.
	_, err = invoke.Run(resolved, "github-issues.get", map[string]any{
		"owner":  "octocat",
		"repo":   "hello-world",
		"number": 1,
	})
	// The tool will fail with a network error (can't reach api.github.com),
	// but critically it should NOT fail with "unsupported import" or
	// "bare import" errors — those would mean esbuild couldn't resolve npm deps.
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "unsupported import") || strings.Contains(errStr, "bare import") {
			t.Fatalf("esbuild failed to resolve npm deps: %v", err)
		}
		if strings.Contains(errStr, "esbuild emit failed") {
			t.Fatalf("esbuild bundling failed: %v", err)
		}
		// Network error or fetch error is expected — the tool tried to call GitHub API
		t.Logf("expected runtime error (network): %v", err)
	}
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

	resolved := toolset.NewResolvedToolset(loaded.ResolvedTools())

	_, err = invoke.Run(resolved, "github-issues.get", map[string]any{
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

	// The bundle should contain code from both zod and octokit
	if !strings.Contains(bundled, "ZodError") {
		t.Error("bundled JS does not contain zod code (expected ZodError)")
	}
	if !strings.Contains(bundled, "Octokit") {
		t.Error("bundled JS does not contain octokit code")
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

// Ensure quickts is used (imported for EmitBundle).
var _ = quickts.EmitBundle
