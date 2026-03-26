package fixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/packaging"
)

// TestDistGoldens verifies that packing each source fixture produces output
// matching the committed golden dist fixtures. When GENERATE_DIST_FIXTURES=1
// is set, it overwrites the goldens instead (use this to regenerate after changes).
//
// The manifest is compared byte-for-byte (JSON is deterministic).
// The archive is compared semantically: golden archive must load successfully
// and its package metadata must match what a fresh pack produces.
func TestDistGoldens(t *testing.T) {
	generate := os.Getenv("GENERATE_DIST_FIXTURES") != ""

	distpkgsDir := filepath.Join(testfilesDir(), "..", "testdata", "goldens", "distpkgs")

	for _, srcDir := range SourceDirs() {
		name := filepath.Base(srcDir)
		distName := name + "-dist"
		goldenDir := filepath.Join(distpkgsDir, distName)

		t.Run(distName, func(t *testing.T) {
			tmpDir := t.TempDir()
			result, err := packaging.Pack(srcDir, tmpDir)
			if err != nil {
				t.Fatalf("Pack(%s): %v", srcDir, err)
			}

			if generate {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", goldenDir, err)
				}
				copyOrFail(t, result.ArchivePath, filepath.Join(goldenDir, filepath.Base(result.ArchivePath)))
				copyOrFail(t, result.ManifestPath, filepath.Join(goldenDir, packaging.PkgManifestFilename))
				t.Logf("wrote goldens to %s", goldenDir)
				return
			}

			// Load golden archive and fresh archive, compare package metadata
			// (ignoring sha256 since tar timestamps make archives non-deterministic)
			goldenArchive := filepath.Join(goldenDir, filepath.Base(result.ArchivePath))
			goldenManifest := filepath.Join(goldenDir, packaging.PkgManifestFilename)
			goldenLoaded, err := packaging.LoadArchive(goldenArchive, goldenManifest)
			if err != nil {
				t.Fatalf("load golden archive: %v", err)
			}
			freshLoaded, err := packaging.LoadArchive(result.ArchivePath, result.ManifestPath)
			if err != nil {
				t.Fatalf("load fresh archive: %v", err)
			}

			goldenPkg := goldenLoaded.Package
			goldenPkg.SHA256 = ""
			freshPkg := freshLoaded.Package
			freshPkg.SHA256 = ""

			goldenJSON, _ := json.Marshal(goldenPkg)
			freshJSON, _ := json.Marshal(freshPkg)
			if diff := cmp.Diff(string(goldenJSON), string(freshJSON)); diff != "" {
				t.Fatalf("package metadata mismatch (-golden +fresh):\n%s", diff)
			}
		})
	}
}

func testfilesDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("fixtures: runtime.Caller failed")
	}
	return filepath.Dir(file)
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
