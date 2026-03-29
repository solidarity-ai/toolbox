package gitfixture

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	pseudoAuthorTimeRFC3339 = "2026-03-27T11:22:33Z"
	pseudoCommitTimeRFC3339 = "2026-03-27T12:34:56Z"
)

// PseudoVersionRepo describes a repository whose HEAD is an untagged commit
// addressable via a Go-style pseudo-version.
type PseudoVersionRepo struct {
	RepoDir         string
	PseudoVersion   string
	PseudoTimestamp string
	ShortCommit     string
	CommitSHA       string
}

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

// CreatePseudoVersionRepoFromDir copies srcDir into a temporary git repository,
// tags the initial commit, adds a second untagged commit with an explicit author
// date, and returns pseudo-version metadata for that HEAD commit.
func CreatePseudoVersionRepoFromDir(t *testing.T, srcDir string) PseudoVersionRepo {
	t.Helper()

	repoDir := CreateTaggedRepoFromDir(t, "v1.0.0", srcDir)

	pseudoNotePath := filepath.Join(repoDir, "PSEUDO_VERSION.txt")
	pseudoNote := []byte("pseudo-version fixture\n")
	if err := os.WriteFile(pseudoNotePath, pseudoNote, 0o644); err != nil {
		t.Fatalf("gitfixture: write %s: %v", pseudoNotePath, err)
	}

	commitTime, err := time.Parse(time.RFC3339, pseudoCommitTimeRFC3339)
	if err != nil {
		t.Fatalf("gitfixture: parse pseudo commit time %q: %v", pseudoCommitTimeRFC3339, err)
	}
	authorTime, err := time.Parse(time.RFC3339, pseudoAuthorTimeRFC3339)
	if err != nil {
		t.Fatalf("gitfixture: parse pseudo author time %q: %v", pseudoAuthorTimeRFC3339, err)
	}

	runGit(t, repoDir, "add", ".")
	runGitWithEnv(t, repoDir, []string{
		"GIT_AUTHOR_DATE=" + authorTime.Format(time.RFC3339),
		"GIT_COMMITTER_DATE=" + commitTime.Format(time.RFC3339),
	}, "-c", "user.name=test", "-c", "user.email=test@test.com", "commit", "-m", "pseudo commit")

	commitSHA := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "HEAD"))
	if len(commitSHA) < 12 {
		t.Fatalf("gitfixture: rev-parse HEAD returned short commit %q", commitSHA)
	}
	shortCommit := commitSHA[:12]

	commitDate := strings.TrimSpace(runGit(t, repoDir, "show", "-s", "--format=%cI", "HEAD"))
	authorDate := strings.TrimSpace(runGit(t, repoDir, "show", "-s", "--format=%aI", "HEAD"))
	commitTimestamp, err := time.Parse(time.RFC3339, commitDate)
	if err != nil {
		t.Fatalf("gitfixture: parse HEAD commit date %q: %v", commitDate, err)
	}
	authorTimestamp, err := time.Parse(time.RFC3339, authorDate)
	if err != nil {
		t.Fatalf("gitfixture: parse HEAD author date %q: %v", authorDate, err)
	}
	if commitTimestamp.UTC().Equal(authorTimestamp.UTC()) {
		t.Fatalf("gitfixture: pseudo fixture author and commit timestamps unexpectedly match: %s", commitDate)
	}
	pseudoTimestamp := commitTimestamp.UTC().Format("20060102150405")

	return PseudoVersionRepo{
		RepoDir:         repoDir,
		PseudoVersion:   fmt.Sprintf("v0.0.0-%s-%s", pseudoTimestamp, shortCommit),
		PseudoTimestamp: pseudoTimestamp,
		ShortCommit:     shortCommit,
		CommitSHA:       commitSHA,
	}
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
	return runGitWithEnv(t, dir, nil, args...)
}

func runGitWithEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
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
