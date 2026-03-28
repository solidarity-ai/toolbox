package toolset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestToolsetLock(t *testing.T) {
	t.Parallel()

	t.Run("DeriveSiblingFilenameFromAnyToolsetFile", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			filename string
			want     string
		}{
			{
				name:     "DefaultToolsetFile",
				filename: filepath.Join("/tmp", "toolbox.toolset.json"),
				want:     filepath.Join("/tmp", "toolbox.toolset.lock"),
			},
			{
				name:     "NamedToolsetFile",
				filename: filepath.Join("/tmp", "support-agent.toolset.json"),
				want:     filepath.Join("/tmp", "support-agent.toolset.lock"),
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := deriveToolsetLockFilename(tt.filename)
				if err != nil {
					t.Fatalf("deriveToolsetLockFilename(%q) error: %v", tt.filename, err)
				}
				if got != tt.want {
					t.Fatalf("deriveToolsetLockFilename(%q) = %q, want %q", tt.filename, got, tt.want)
				}
			})
		}
	})

	t.Run("RejectsUnsupportedFilename", func(t *testing.T) {
		t.Parallel()

		_, err := deriveToolsetLockFilename(filepath.Join("/tmp", "toolbox.json"))
		if err == nil {
			t.Fatal("deriveToolsetLockFilename() error = nil, want non-nil")
		}
		assertErrorContains(t, err, "toolbox.json")
		assertErrorContains(t, err, ".toolset.json")
	})

	t.Run("LoadMissingFileReturnsReadErrorWithFilename", func(t *testing.T) {
		t.Parallel()

		filename := filepath.Join(t.TempDir(), "toolbox.toolset.lock")
		got, err := LoadLock(filename)
		if err == nil {
			t.Fatal("LoadLock() error = nil, want non-nil")
		}
		if got != nil {
			t.Fatalf("LoadLock() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "read toolset lock file")
		assertErrorContains(t, err, filename)
	})

	t.Run("InvalidJSONReturnsParseError", func(t *testing.T) {
		t.Parallel()

		filename := filepath.Join(t.TempDir(), "toolbox.toolset.lock")
		if err := os.WriteFile(filename, []byte("{bad"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", filename, err)
		}

		got, err := LoadLock(filename)
		if err == nil {
			t.Fatal("LoadLock() error = nil, want non-nil")
		}
		if got != nil {
			t.Fatalf("LoadLock() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "parse toolset lock file")
		assertErrorContains(t, err, filename)
	})

	t.Run("SchemaValidationRejectsWrongShapeBeforeSemanticValidation", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.lock", map[string]any{
			"packages": map[string]any{
				"example.com/acme/calc@v1.2.3": map[string]any{
					"archive_sha256": strings.Repeat("a", 64),
					"git_sha":        strings.Repeat("b", 40),
					"resolved_from":  ToolsetLockResolvedFromGitHubRelease,
				},
			},
		})

		got, err := LoadLock(filename)
		if err == nil {
			t.Fatal("LoadLock() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("LoadLock() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "schema-validate toolset lock file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, "resolved_at")
	})

	t.Run("SemanticValidationRejectsInvalidPackageKeyWithEntryContext", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.lock", map[string]any{
			"packages": map[string]any{
				"not-a-package-key": map[string]any{
					"archive_sha256": strings.Repeat("a", 64),
					"git_sha":        strings.Repeat("b", 40),
					"resolved_from":  ToolsetLockResolvedFromGitHubRelease,
					"resolved_at":    "2026-03-27T20:15:00Z",
				},
			},
		})

		got, err := LoadLock(filename)
		if err == nil {
			t.Fatal("LoadLock() error = nil, want semantic validation error")
		}
		if got != nil {
			t.Fatalf("LoadLock() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "validate toolset lock file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, `packages["not-a-package-key"]`)
		assertErrorContains(t, err, "package version")
		if strings.Contains(err.Error(), "schema-validate toolset lock file") {
			t.Fatalf("error = %q, want semantic validation error instead of schema validation", err.Error())
		}
	})

	t.Run("SemanticValidationRejectsInvalidProvenanceFields", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.lock", map[string]any{
			"packages": map[string]any{
				"example.com/acme/calc@v1.2.3": map[string]any{
					"archive_sha256": strings.Repeat("a", 64),
					"git_sha":        "not-a-commit",
					"resolved_from":  "side-channel",
					"resolved_at":    "not-a-time",
				},
			},
		})

		got, err := LoadLock(filename)
		if err == nil {
			t.Fatal("LoadLock() error = nil, want semantic validation error")
		}
		if got != nil {
			t.Fatalf("LoadLock() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "validate toolset lock file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, `packages["example.com/acme/calc@v1.2.3"]`)
	})

	t.Run("WriteAndLoadRoundTripPreservesDeterministicPackageOrdering", func(t *testing.T) {
		t.Parallel()

		lock := &ToolsetLockFile{
			Packages: map[string]ToolsetLockEntry{
				"example.com/zeta/echo@v2.0.0": {
					ArchiveSHA256: strings.Repeat("e", 64),
					GitSHA:        strings.Repeat("f", 40),
					ResolvedFrom:  ToolsetLockResolvedFromGitSource,
					ResolvedAt:    "2026-03-27T22:18:00Z",
				},
				"example.com/acme/calc@v1.2.3": {
					ArchiveSHA256: strings.Repeat("a", 64),
					GitSHA:        strings.Repeat("b", 40),
					ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
					ResolvedAt:    "2026-03-27T20:15:00Z",
				},
			},
		}

		filename := filepath.Join(t.TempDir(), "support-agent.toolset.lock")
		if err := lock.Write(filename); err != nil {
			t.Fatalf("Write(%q): %v", filename, err)
		}

		raw, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", filename, err)
		}
		text := string(raw)
		if strings.Index(text, `"example.com/acme/calc@v1.2.3"`) > strings.Index(text, `"example.com/zeta/echo@v2.0.0"`) {
			t.Fatalf("lockfile package order is not deterministic:\n%s", text)
		}

		reloaded, err := LoadLock(filename)
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", filename, err)
		}
		if !reflect.DeepEqual(reloaded.Packages, lock.Packages) {
			t.Fatalf("reloaded packages = %#v, want %#v", reloaded.Packages, lock.Packages)
		}

		second := filepath.Join(t.TempDir(), "toolbox.toolset.lock")
		if err := reloaded.Write(second); err != nil {
			t.Fatalf("Write(%q): %v", second, err)
		}
		secondRaw, err := os.ReadFile(second)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", second, err)
		}
		if string(secondRaw) != text {
			t.Fatalf("second write changed lockfile bytes:\nfirst:\n%s\nsecond:\n%s", text, string(secondRaw))
		}
	})
}

func writeJSONFile(t *testing.T, basename string, value any) string {
	t.Helper()

	filename := filepath.Join(t.TempDir(), basename)
	data, err := jsonMarshal(value)
	if err != nil {
		t.Fatalf("jsonMarshal: %v", err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filename, err)
	}
	return filename
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}
