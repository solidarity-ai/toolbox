package toolsetfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestToolsetLocal(t *testing.T) {
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
				want:     filepath.Join("/tmp", "toolbox.toolset.local.json"),
			},
			{
				name:     "NamedToolsetFile",
				filename: filepath.Join("/tmp", "support-agent.toolset.json"),
				want:     filepath.Join("/tmp", "support-agent.toolset.local.json"),
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := deriveToolsetLocalFilename(tt.filename)
				if err != nil {
					t.Fatalf("deriveToolsetLocalFilename(%q) error: %v", tt.filename, err)
				}
				if got != tt.want {
					t.Fatalf("deriveToolsetLocalFilename(%q) = %q, want %q", tt.filename, got, tt.want)
				}
			})
		}
	})

	t.Run("RejectsUnsupportedFilename", func(t *testing.T) {
		t.Parallel()

		_, err := deriveToolsetLocalFilename(filepath.Join("/tmp", "toolbox.json"))
		if err == nil {
			t.Fatal("deriveToolsetLocalFilename() error = nil, want non-nil")
		}
		assertErrorContains(t, err, "toolbox.json")
		assertErrorContains(t, err, ".toolset.json")
	})

	t.Run("MissingOverlayForLoadedToolsetReturnsNil", func(t *testing.T) {
		t.Parallel()

		toolsetFile := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{{
				"tool": "example.com/acme/calc@v1.2.3/calc.add",
			}},
		})

		got, err := toolsetFile.LoadLocal()
		if err != nil {
			t.Fatalf("LoadLocal() error: %v", err)
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
	})

	t.Run("InvalidJSONReturnsParseError", func(t *testing.T) {
		t.Parallel()

		filename := filepath.Join(t.TempDir(), "toolbox.toolset.local.json")
		if err := os.WriteFile(filename, []byte("{bad"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", filename, err)
		}

		got, err := LoadLocal(filename)
		if err == nil {
			t.Fatal("LoadLocal() error = nil, want non-nil")
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "parse toolset local file")
		assertErrorContains(t, err, filename)
	})

	t.Run("SchemaValidationRejectsUnknownTopLevelField", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.local.json", map[string]any{
			"replace": map[string]string{
				"example.com/acme/calc": "../calc",
			},
			"unexpected": true,
		})

		got, err := LoadLocal(filename)
		if err == nil {
			t.Fatal("LoadLocal() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "schema-validate toolset local file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, "unexpected")
	})

	t.Run("SchemaValidationRejectsWrongReplaceShapeBeforeSemanticValidation", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.local.json", map[string]any{
			"replace": []string{"example.com/acme/calc"},
		})

		got, err := LoadLocal(filename)
		if err == nil {
			t.Fatal("LoadLocal() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "schema-validate toolset local file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, "replace")
		if strings.Contains(err.Error(), `replace["example.com/acme/calc"]`) {
			t.Fatalf("error = %q, want schema validation before semantic validation", err.Error())
		}
	})

	t.Run("SchemaValidationRejectsWrongReplaceValueType", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.local.json", map[string]any{
			"replace": map[string]any{
				"example.com/acme/calc": map[string]any{"dir": "../calc"},
			},
		})

		got, err := LoadLocal(filename)
		if err == nil {
			t.Fatal("LoadLocal() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "schema-validate toolset local file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, "replace")
	})

	t.Run("SemanticValidationRejectsInvalidModuleKeyWithEntryContext", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.local.json", map[string]any{
			"replace": map[string]string{
				"bad": "../calc",
			},
		})

		got, err := LoadLocal(filename)
		if err == nil {
			t.Fatal("LoadLocal() error = nil, want semantic validation error")
		}
		if got != nil {
			t.Fatalf("LoadLocal() = %#v, want nil", got)
		}
		assertErrorContains(t, err, "validate toolset local file")
		assertErrorContains(t, err, filename)
		assertErrorContains(t, err, `replace["bad"]`)
		assertErrorContains(t, err, "module path")
		if strings.Contains(err.Error(), "schema-validate toolset local file") {
			t.Fatalf("error = %q, want semantic validation error instead of schema validation", err.Error())
		}
	})

	t.Run("LoadRoundTripPreservesReplaceMapAndParsedLookup", func(t *testing.T) {
		t.Parallel()

		filename := writeJSONFile(t, "toolbox.toolset.local.json", map[string]any{
			"replace": map[string]string{
				"example.com/zeta/echo": "../echo",
				"example.com/acme/calc": "../calc",
			},
		})

		got, err := LoadLocal(filename)
		if err != nil {
			t.Fatalf("LoadLocal() error: %v", err)
		}
		wantReplace := map[string]string{
			"example.com/zeta/echo": "../echo",
			"example.com/acme/calc": "../calc",
		}
		if !reflect.DeepEqual(got.Replace, wantReplace) {
			t.Fatalf("Replace = %#v, want %#v", got.Replace, wantReplace)
		}
		if got.SourceFilename() != filename {
			t.Fatalf("SourceFilename() = %q, want %q", got.SourceFilename(), filename)
		}

		module, err := tooldef.ParseModulePath("example.com/acme/calc")
		if err != nil {
			t.Fatalf("ParseModulePath(): %v", err)
		}
		replaceDir, ok := got.ReplacementDir(module)
		if !ok {
			t.Fatalf("ReplacementDir(%q) ok = false, want true", module)
		}
		if replaceDir != "../calc" {
			t.Fatalf("ReplacementDir(%q) = %q, want %q", module, replaceDir, "../calc")
		}
		replaceDirAbs, ok := got.ReplacementDirAbs(module)
		if !ok {
			t.Fatalf("ReplacementDirAbs(%q) ok = false, want true", module)
		}
		wantAbs := filepath.Join(filepath.Dir(filename), "..", "calc")
		if replaceDirAbs != filepath.Clean(wantAbs) {
			t.Fatalf("ReplacementDirAbs(%q) = %q, want %q", module, replaceDirAbs, filepath.Clean(wantAbs))
		}
	})
}
