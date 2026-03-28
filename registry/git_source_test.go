package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/registry/testutil/gitfixture"
)

func TestGitSource(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		repoDir := gitfixture.CreateTaggedRepoFromDir(t, "v1.0.0", fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		module := ModulePath(repoDir)
		version := mustVersion(t, "v1.0.0")

		archiveBytes, manifestBytes, err := src.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if len(archiveBytes) == 0 {
			t.Fatal("Fetch(): archive bytes were empty")
		}
		if len(manifestBytes) == 0 {
			t.Fatal("Fetch(): manifest bytes were empty")
		}
	})

	t.Run("pseudo_version_happy_path", func(t *testing.T) {
		meta := gitfixture.CreatePseudoVersionRepoFromDir(t, fixtureSourceDir(t, "calc"))

		src := &GitSourceFallback{URLPrefix: "file://"}
		module := ModulePath(meta.RepoDir)
		version := mustVersion(t, meta.PseudoVersion)

		archiveBytes, manifestBytes, err := src.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if len(archiveBytes) == 0 {
			t.Fatal("Fetch(): archive bytes were empty")
		}
		if len(manifestBytes) == 0 {
			t.Fatal("Fetch(): manifest bytes were empty")
		}
		if !strings.Contains(string(manifestBytes), "\"name\": \"calc\"") {
			t.Fatalf("Fetch(): manifest bytes did not contain calc package name: %s", string(manifestBytes))
		}
	})

	t.Run("missing_tag", func(t *testing.T) {
		repoDir := gitfixture.CreateRepoMissingTag(t, "v1.0.0", "v9.9.9")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v9.9.9"))
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

		_, _, err := src.Fetch(context.Background(), ModulePath(meta.RepoDir), missingVersion)
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

		_, _, err := src.Fetch(context.Background(), ModulePath(meta.RepoDir), mismatchVersion)
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
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v1.0.0"))
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
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), mustVersion(t, "v1.0.0"))
		if err == nil {
			t.Fatal("Fetch(): expected error for corrupt toolbox.devpkg.json")
		}
		if !strings.Contains(err.Error(), "pack cloned repo") {
			t.Fatalf("Fetch(): expected pack error for corrupt package, got %v", err)
		}
	})
}
