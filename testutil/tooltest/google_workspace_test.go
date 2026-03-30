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
