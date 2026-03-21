package fixtures

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
)

// TestGenerateDistFixtures is a helper that regenerates the *-dist golden
// fixtures from the source dev fixtures. Run with:
//
//	go test ./testutil/fixtures/ -run TestGenerateDistFixtures -v
//
// The generated fixtures are committed to the repo as golden masters.
func TestGenerateDistFixtures(t *testing.T) {
	if os.Getenv("GENERATE_DIST_FIXTURES") == "" {
		t.Skip("set GENERATE_DIST_FIXTURES=1 to regenerate dist golden fixtures")
	}

	for _, srcDir := range SourceDirs() {
		name := filepath.Base(srcDir)
		distName := name + "-dist"
		distDir := filepath.Join(filepath.Dir(srcDir), distName)

		t.Run(distName, func(t *testing.T) {
			if err := os.MkdirAll(distDir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", distDir, err)
			}

			result, err := packaging.Pack(srcDir, distDir)
			if err != nil {
				t.Fatalf("Pack(%s): %v", srcDir, err)
			}

			t.Logf("wrote %s", result.ArchivePath)
			t.Logf("wrote %s", result.ManifestPath)
		})
	}
}
