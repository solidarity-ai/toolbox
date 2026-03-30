package tooltest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyGoogleWorkspaceFixtureCopiesManifestAndTool(t *testing.T) {
	dir := CopyGoogleWorkspaceFixture(t)
	for _, rel := range []string{"toolbox.devpkg.json", filepath.Join("tools", "users.list.ts")} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("copied fixture missing %s: %v", rel, err)
		}
	}
}

func TestAssertPreparedGoogleWorkspaceFixtureContract(t *testing.T) {
	harness := NewGoogleAuthHarness(t)
	var baseURL string
	baseURL = harness.Server.AuthBaseURL()
	preparedDir := PrepareGoogleWorkspaceFixture(t, baseURL, harness.Provider)

	AssertPreparedGoogleWorkspaceFixtureContract(t, preparedDir, baseURL, harness.Provider)
}
