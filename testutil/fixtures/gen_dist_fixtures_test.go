package fixtures

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
)

// TestDistGoldens verifies that packing each source fixture produces output
// identical to the committed golden dist fixtures. When GENERATE_DIST_FIXTURES=1
// is set, it overwrites the goldens instead (use this to regenerate after changes).
func TestDistGoldens(t *testing.T) {
	generate := os.Getenv("GENERATE_DIST_FIXTURES") != ""

	for _, srcDir := range SourceDirs() {
		name := filepath.Base(srcDir)
		distName := name + "-dist"
		goldenDir := filepath.Join(filepath.Dir(srcDir), distName)

		t.Run(distName, func(t *testing.T) {
			// Pack to a temp dir
			tmpDir := t.TempDir()
			result, err := packaging.Pack(srcDir, tmpDir)
			if err != nil {
				t.Fatalf("Pack(%s): %v", srcDir, err)
			}

			if generate {
				// Overwrite goldens
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", goldenDir, err)
				}
				copyOrFail(t, result.ArchivePath, filepath.Join(goldenDir, filepath.Base(result.ArchivePath)))
				copyOrFail(t, result.ManifestPath, filepath.Join(goldenDir, packaging.PkgManifestFilename))
				t.Logf("wrote goldens to %s", goldenDir)
				return
			}

			// Compare against committed goldens
			goldenArchive := filepath.Join(goldenDir, filepath.Base(result.ArchivePath))
			goldenManifest := filepath.Join(goldenDir, packaging.PkgManifestFilename)

			assertFilesEqual(t, "archive", goldenArchive, result.ArchivePath)
			assertFilesEqual(t, "manifest", goldenManifest, result.ManifestPath)
		})
	}
}

func assertFilesEqual(t *testing.T, label, goldenPath, actualPath string) {
	t.Helper()

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s %s: %v", label, goldenPath, err)
	}
	actual, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatalf("read actual %s %s: %v", label, actualPath, err)
	}

	if string(golden) != string(actual) {
		t.Fatalf("%s mismatch for %s: golden %d bytes vs actual %d bytes — regenerate with GENERATE_DIST_FIXTURES=1",
			label, filepath.Base(goldenPath), len(golden), len(actual))
	}
}

func copyOrFail(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
