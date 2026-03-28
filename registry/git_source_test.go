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
		version := Version("v1.0.0")

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

	t.Run("missing_tag", func(t *testing.T) {
		repoDir := gitfixture.CreateRepoMissingTag(t, "v1.0.0", "v9.9.9")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), Version("v9.9.9"))
		if err == nil {
			t.Fatal("Fetch(): expected error for missing tag")
		}
		if !strings.Contains(err.Error(), "git clone") {
			t.Fatalf("Fetch(): expected git clone error for missing tag, got %v", err)
		}
	})

	t.Run("broken_repo", func(t *testing.T) {
		repoDir := gitfixture.CreateBrokenRepo(t, "v1.0.0")

		src := &GitSourceFallback{URLPrefix: "file://"}
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), Version("v1.0.0"))
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
		_, _, err := src.Fetch(context.Background(), ModulePath(repoDir), Version("v1.0.0"))
		if err == nil {
			t.Fatal("Fetch(): expected error for corrupt toolbox.devpkg.json")
		}
		if !strings.Contains(err.Error(), "pack cloned repo") {
			t.Fatalf("Fetch(): expected pack error for corrupt package, got %v", err)
		}
	})
}
