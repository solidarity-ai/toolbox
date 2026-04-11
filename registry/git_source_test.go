package registry

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/registry/testutil/gitfixture"
)

func TestGitSource(t *testing.T) {
	t.Run("list_versions", func(t *testing.T) {
		repoDir := gitfixture.CreateTaggedRepoFromDir(t, "v1.0.0", fixtureSourceDir(t, "calc"))
		gitfixture.AddCommitAndTag(t, repoDir, "v1.2.0", map[string][]byte{"README.md": []byte("v1.2.0\n")}, "v1.2.0")
		gitfixture.AddCommitAndTag(t, repoDir, "v1.1.0", map[string][]byte{"README.md": []byte("v1.1.0\n")}, "v1.1.0")
		gitfixture.AddCommitAndTag(t, repoDir, "not-a-version", map[string][]byte{"README.md": []byte("invalid tag\n")}, "invalid tag")

		src := &GitSourceFallback{URLPrefix: "file://"}
		versions, err := src.ListVersions(context.Background(), ModulePath(repoDir))
		if err != nil {
			t.Fatalf("ListVersions(): %v", err)
		}
		got := []string{versions[0].String(), versions[1].String(), versions[2].String()}
		want := []string{"v1.2.0", "v1.1.0", "v1.0.0"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("versions = %#v, want %#v", got, want)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		repoDir := gitfixture.CreateTaggedRepoFromDir(t, "v1.0.0", fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		module := ModulePath(repoDir)
		version := mustVersion(t, "v1.0.0")

		result, err := src.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if len(result.Archive) == 0 {
			t.Fatal("Fetch(): archive bytes were empty")
		}
		if len(result.Manifest) == 0 {
			t.Fatal("Fetch(): manifest bytes were empty")
		}
		if result.Metadata.ResolvedFrom != ResolvedFromGitSource {
			t.Fatalf("resolved_from = %q, want %q", result.Metadata.ResolvedFrom, ResolvedFromGitSource)
		}
		if result.Metadata.ArchiveSHA256 != sha256Hex(result.Archive) {
			t.Fatalf("archive_sha256 = %q, want %q", result.Metadata.ArchiveSHA256, sha256Hex(result.Archive))
		}
		if len(result.Metadata.GitSHA) != 40 {
			t.Fatalf("git_sha = %q, want 40-char commit", result.Metadata.GitSHA)
		}
		if _, err := time.Parse(time.RFC3339, result.Metadata.ResolvedAt); err != nil {
			t.Fatalf("resolved_at parse error: %v", err)
		}
	})

	t.Run("pseudo_version_happy_path", func(t *testing.T) {
		meta := gitfixture.CreatePseudoVersionRepoFromDir(t, fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		module := ModulePath(meta.RepoDir)
		version := mustVersion(t, meta.PseudoVersion)

		result, err := src.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if len(result.Archive) == 0 {
			t.Fatal("Fetch(): archive bytes were empty")
		}
		if len(result.Manifest) == 0 {
			t.Fatal("Fetch(): manifest bytes were empty")
		}
		if !strings.Contains(string(result.Manifest), "\"name\": \"calc\"") {
			t.Fatalf("Fetch(): manifest bytes did not contain calc package name: %s", string(result.Manifest))
		}
		if result.Metadata.GitSHA != strings.ToLower(meta.CommitSHA) {
			t.Fatalf("git_sha = %q, want %q", result.Metadata.GitSHA, strings.ToLower(meta.CommitSHA))
		}
	})

	t.Run("missing_tag", func(t *testing.T) {
		repoDir := gitfixture.CreateRepoMissingTag(t, "v1.0.0", "v9.9.9")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v9.9.9"))
		if err == nil {
			t.Fatal("Fetch(): expected error for missing tag")
		}
		if !strings.Contains(err.Error(), "git clone") {
			t.Fatalf("Fetch(): expected git clone error for missing tag, got %v", err)
		}
	})

	t.Run("pseudo_version_missing_commit_prefix", func(t *testing.T) {
		meta := gitfixture.CreatePseudoVersionRepoFromDir(t, fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		missingVersion := mustVersion(t, "v0.0.0-"+meta.PseudoTimestamp+"-deadbeefcafe")

		_, err := src.Fetch(context.Background(), ModulePath(meta.RepoDir), missingVersion)
		if err == nil {
			t.Fatal("Fetch(): expected error for missing pseudo-version commit prefix")
		}
		if !strings.Contains(err.Error(), "git rev-parse") {
			t.Fatalf("Fetch(): expected git rev-parse error for missing commit prefix, got %v", err)
		}
	})

	t.Run("pseudo_version_timestamp_mismatch", func(t *testing.T) {
		meta := gitfixture.CreatePseudoVersionRepoFromDir(t, fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		mismatchVersion := mustVersion(t, "v0.0.0-19700101000000-"+meta.ShortCommit)

		_, err := src.Fetch(context.Background(), ModulePath(meta.RepoDir), mismatchVersion)
		if err == nil {
			t.Fatal("Fetch(): expected error for pseudo-version timestamp mismatch")
		}
		if !strings.Contains(err.Error(), "timestamp mismatch") {
			t.Fatalf("Fetch(): expected timestamp mismatch error, got %v", err)
		}
		if strings.Contains(err.Error(), "pack cloned repo") {
			t.Fatalf("Fetch(): expected timestamp mismatch before packaging, got %v", err)
		}
	})

	t.Run("broken_repo", func(t *testing.T) {
		repoDir := gitfixture.CreateBrokenRepo(t, "v1.0.0")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v1.0.0"))
		if err == nil {
			t.Fatal("Fetch(): expected error for repo missing toolbox.devpkg.json")
		}
		if !strings.Contains(err.Error(), "pack cloned repo") {
			t.Fatalf("Fetch(): expected pack error for broken repo, got %v", err)
		}
	})

	t.Run("corrupt_package", func(t *testing.T) {
		repoDir := gitfixture.CreateCorruptPackageRepo(t, "v1.0.0")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v1.0.0"))
		if err == nil {
			t.Fatal("Fetch(): expected error for corrupt toolbox.devpkg.json")
		}
		if !strings.Contains(err.Error(), "pack cloned repo") {
			t.Fatalf("Fetch(): expected pack error for corrupt package, got %v", err)
		}
	})

	t.Run("missing_repository_is_classified_as_not_found", func(t *testing.T) {
		err := wrapGitCommandError(
			"git ls-remote tags github.com/admin/stub from https://github.com/admin/stub",
			errors.New("exit status 128"),
			"remote: Repository not found.\nfatal: repository 'https://github.com/admin/stub/' not found",
		)
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("error = %v, want ErrReleaseNotFound", err)
		}
	})
}
