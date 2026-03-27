package gitfixture

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/testutil/fixtures"
)

func TestCreateTaggedRepo(t *testing.T) {
	repoDir := CreateTaggedRepo(t, "v1.0.0", map[string][]byte{
		"hello.txt": []byte("hello"),
	})

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)

	tags := runGit(t, cloneDir, "tag", "-l")
	if !strings.Contains(tags, "v1.0.0") {
		t.Fatalf("git tag -l missing v1.0.0: %q", tags)
	}
	if out := runGit(t, cloneDir, "checkout", "v1.0.0"); out != "" {
		t.Logf("git checkout v1.0.0 output: %s", strings.TrimSpace(out))
	}

	content, err := os.ReadFile(filepath.Join(cloneDir, "hello.txt"))
	if err != nil {
		t.Fatalf("read hello.txt: %v", err)
	}
	if string(content) != "hello" {
		t.Fatalf("hello.txt mismatch: want %q, got %q", "hello", string(content))
	}

	t.Logf("created repo %s tagged v1.0.0", repoDir)
	t.Logf("cloned repo into %s and verified hello.txt", cloneDir)
}

func TestCreateTaggedRepoFromDir(t *testing.T) {
	srcDir := fixtureSourceDir(t, "calc")
	repoDir := CreateTaggedRepoFromDir(t, "v2.0.0", srcDir)

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)
	runGit(t, cloneDir, "checkout", "v2.0.0")

	manifestPath := filepath.Join(cloneDir, "toolbox.devpkg.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	if !strings.Contains(string(manifest), `"name": "calc"`) {
		t.Fatalf("manifest missing calc name: %s", manifest)
	}

	t.Logf("created repo %s from fixture dir %s", repoDir, srcDir)
	t.Logf("checked out v2.0.0 and verified toolbox.devpkg.json")
}

func TestMultipleTags(t *testing.T) {
	repoDir := CreateTaggedRepo(t, "v1.0.0", map[string][]byte{
		"version.txt": []byte("one\n"),
	})
	AddCommitAndTag(t, repoDir, "v2.0.0", map[string][]byte{
		"version.txt": []byte("two\n"),
	}, "second")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)

	tags := runGit(t, cloneDir, "tag", "-l")
	if !strings.Contains(tags, "v1.0.0") || !strings.Contains(tags, "v2.0.0") {
		t.Fatalf("git tag -l missing expected tags: %q", tags)
	}

	runGit(t, cloneDir, "checkout", "v1.0.0")
	first, err := os.ReadFile(filepath.Join(cloneDir, "version.txt"))
	if err != nil {
		t.Fatalf("read version.txt at v1.0.0: %v", err)
	}

	runGit(t, cloneDir, "checkout", "v2.0.0")
	second, err := os.ReadFile(filepath.Join(cloneDir, "version.txt"))
	if err != nil {
		t.Fatalf("read version.txt at v2.0.0: %v", err)
	}

	if string(first) == string(second) {
		t.Fatalf("version.txt did not change across tags: v1=%q v2=%q", first, second)
	}

	t.Logf("verified tags in cloned repo: %s", strings.TrimSpace(tags))
	t.Logf("version.txt differs between v1.0.0=%q and v2.0.0=%q", strings.TrimSpace(string(first)), strings.TrimSpace(string(second)))
}

func TestCreateBrokenRepo(t *testing.T) {
	repoDir := CreateBrokenRepo(t, "v0.1.0")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)
	runGit(t, cloneDir, "checkout", "v0.1.0")

	listing := runGit(t, cloneDir, "ls-files")
	if strings.Contains(listing, "toolbox.devpkg.json") {
		t.Fatalf("broken repo unexpectedly contains toolbox.devpkg.json: %q", listing)
	}
	if !strings.Contains(listing, "README.md") {
		t.Fatalf("broken repo missing README.md: %q", listing)
	}
}

func TestCreateRepoMissingTag(t *testing.T) {
	repoDir := CreateRepoMissingTag(t, "v1.0.0", "v9.9.9")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)
	runGit(t, cloneDir, "checkout", "v1.0.0")

	missing := strings.TrimSpace(runGit(t, cloneDir, "tag", "-l", "v9.9.9"))
	if missing != "" {
		t.Fatalf("expected missing tag lookup to be empty, got %q", missing)
	}
}

func TestCreateCorruptPackageRepo(t *testing.T) {
	repoDir := CreateCorruptPackageRepo(t, "v0.2.0")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGit(t, ".", "clone", repoDir, cloneDir)
	runGit(t, cloneDir, "checkout", "v0.2.0")

	manifestPath := filepath.Join(cloneDir, "toolbox.devpkg.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(manifest, &decoded); err == nil {
		t.Fatalf("expected invalid JSON in %s, but unmarshal succeeded", manifestPath)
	} else if !errors.As(err, new(*json.SyntaxError)) {
		t.Fatalf("expected json.SyntaxError, got %T: %v", err, err)
	}
}

func fixtureSourceDir(t *testing.T, name string) string {
	t.Helper()

	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == name {
			return dir
		}
	}
	t.Fatalf("fixture source dir %q not found", name)
	return ""
}
