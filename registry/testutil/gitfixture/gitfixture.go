package gitfixture

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// CreateTaggedRepo creates a temporary git repository with the provided files,
// commits them, tags the initial commit, and returns the repository path.
func CreateTaggedRepo(t *testing.T, tag string, files map[string][]byte) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")

	for relPath, content := range files {
		path := filepath.Join(repoDir, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("gitfixture: mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("gitfixture: write %s: %v", path, err)
		}
	}

	commitAllAndTag(t, repoDir, tag)
	return repoDir
}

// CreateTaggedRepoFromDir copies all files from srcDir into a temporary git
// repository, commits them, tags the initial commit, and returns the repo path.
func CreateTaggedRepoFromDir(t *testing.T, tag string, srcDir string) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init")

	if err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		targetPath := filepath.Join(repoDir, relPath)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, data, 0o644)
	}); err != nil {
		t.Fatalf("gitfixture: copy %s into repo: %v", srcDir, err)
	}

	commitAllAndTag(t, repoDir, tag)
	return repoDir
}

// CreateBrokenRepo creates a tagged repository without toolbox.devpkg.json so
// resolver tests can verify missing-manifest failures.
func CreateBrokenRepo(t *testing.T, tag string) string {
	t.Helper()

	return CreateTaggedRepo(t, tag, map[string][]byte{
		"README.md": []byte("# broken fixture\n"),
	})
}

// CreateRepoMissingTag creates a repository tagged only at existingTag. Tests
// can attempt to resolve or checkout missingTag, which intentionally does not
// exist in the repository.
func CreateRepoMissingTag(t *testing.T, existingTag string, missingTag string) string {
	t.Helper()

	_ = missingTag
	return CreateTaggedRepo(t, existingTag, map[string][]byte{
		"toolbox.devpkg.json": []byte("{\n  \"name\": \"missing-tag\",\n  \"version\": \"1.0.0\"\n}\n"),
		"README.md":           []byte("# missing tag fixture\n"),
	})
}

// CreateCorruptPackageRepo creates a tagged repository whose
// toolbox.devpkg.json contains malformed JSON.
func CreateCorruptPackageRepo(t *testing.T, tag string) string {
	t.Helper()

	return CreateTaggedRepo(t, tag, map[string][]byte{
		"toolbox.devpkg.json": []byte("{broken"),
		"README.md":           []byte("# corrupt package fixture\n"),
	})
}

func commitAllAndTag(t *testing.T, repoDir string, tag string) {
	t.Helper()

	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "-c", "user.name=test", "-c", "user.email=test@test.com", "commit", "-m", "initial")
	runGit(t, repoDir, "tag", tag)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gitfixture: git %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

// AddCommitAndTag writes updated files into an existing repository, commits them,
// and creates a new tag. Tests use this to verify multi-tag histories.
func AddCommitAndTag(t *testing.T, repoDir string, tag string, files map[string][]byte, message string) {
	t.Helper()

	for relPath, content := range files {
		path := filepath.Join(repoDir, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("gitfixture: mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("gitfixture: write %s: %v", path, err)
		}
	}

	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "-c", "user.name=test", "-c", "user.email=test@test.com", "commit", "-m", message)
	runGit(t, repoDir, "tag", tag)
}
