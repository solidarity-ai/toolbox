package packaging

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type loadPackageTestCase struct {
	name         string
	fileName     string
	load         func(string, ValidationMode) (LoadResult, error)
	mode         ValidationMode
	manifest     string
	wantPackage  tooldef.Package
	wantWarnings int
	wantErr      string
}

func TestLoadPackageFromDir(t *testing.T) {
	t.Parallel()

	tests := baseLoadPackageTestCases()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runLoadPackageTestCase(t, tt)
		})
	}
}

// TODO: when building for distribution a package should issue warnings/or compilation errors if the inferred fields have not been added.
// i.e. don't do the inference if the ValidationMode is ValidationModeBuild
// This follows for comments as well.

func runLoadPackageTestCase(t *testing.T, tt loadPackageTestCase) {
	t.Helper()

	dir := t.TempDir()
	fileName := tt.fileName
	if fileName == "" {
		fileName = "toolbox.pkg.json"
	}
	load := tt.load
	if load == nil {
		load = LoadPackageFromDirWithMode
	}
	manifestPath := filepath.Join(dir, fileName)
	if err := os.WriteFile(manifestPath, []byte(tt.manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	result, err := load(dir, tt.mode)
	if tt.wantErr != "" {
		if err == nil {
			t.Fatalf("expected error containing %q", tt.wantErr)
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
		}
		return
	}

	if err != nil {
		t.Fatalf("load package dir: %v", err)
	}
	if len(result.Warnings) != tt.wantWarnings {
		t.Fatalf("expected %d warnings, got %d", tt.wantWarnings, len(result.Warnings))
	}
	if diff := cmp.Diff(tt.wantPackage, result.Package); diff != "" {
		t.Fatalf("LoadPackageFromDir() mismatch (-want +got):\n%s", diff)
	}
}

func baseLoadPackageTestCases() []loadPackageTestCase {
	sourceTests := []loadPackageTestCase{
		{
			name: "valid dev",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "additional typescript globs pass through",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "additionalTypeScriptGlobs": ["lib/**/*.ts"],
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "wasmer runtime allows executables",
			mode: ValidationModeDev,
			manifest: `{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": {
    "gwc": "dist/gwc.wasm"
  },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "google-workspace",
				Runtime: tooldef.RuntimeTypeScriptWasixSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "typescript runtime rejects executables",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "executables": {
    "gwc": "dist/gwc.wasm"
  },
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantErr: `executables`,
		},
		{
			name: "valid dist",
			mode: ValidationModeDist,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "missing name",
			mode: ValidationModeDev,
			manifest: `{
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			wantErr: `"name"`,
		},
		{
			name: "missing runtime",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "tools": [
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			wantErr: `"runtime"`,
		},
		{
			name: "missing idempotent warns in dev",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", AccessMode: tooldef.AccessModeReadOnly},
				},
			},
			wantWarnings: 1,
		},
		{
			name: "missing idempotent errors in dist",
			mode: ValidationModeDist,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "accessMode": "appendOnly" }
  ]
}`,
			wantErr: `"idempotent"`,
		},
		{
			name: "missing accessMode warns in dev",
			mode: ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeAppendOnly},
				},
			},
			wantWarnings: 0,
		},
		{
			name: "missing accessMode compiles and passes in dist",
			mode: ValidationModeDist,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeAppendOnly},
				},
			},
		},
	}
	builtTests := []loadPackageTestCase{
		{
			name:     "valid built dev",
			fileName: "toolbox.pkg.compiled.json",
			load:     LoadBuiltDirWithMode,
			mode:     ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name:     "built missing idempotent warns in dev",
			fileName: "toolbox.pkg.compiled.json",
			load:     LoadBuiltDirWithMode,
			mode:     ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "accessMode": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", AccessMode: tooldef.AccessModeReadOnly},
				},
			},
			wantWarnings: 1,
		},
		{
			name:     "built missing accessMode warns in dev",
			fileName: "toolbox.pkg.compiled.json",
			load:     LoadBuiltDirWithMode,
			mode:     ValidationModeDev,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			wantWarnings: 1,
		},
		{
			name:     "built missing accessMode errors in dist",
			fileName: "toolbox.pkg.compiled.json",
			load:     LoadBuiltDirWithMode,
			mode:     ValidationModeDist,
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true }
  ]
}`,
			wantErr: `"accessMode"`,
		},
	}
	return append(sourceTests, builtTests...)
}

func TestLoadSourcePackageRestrictsTypeScriptFilesToManifestAndGlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		manifest     string
		wantHelperOK bool
	}{
		{
			name: "tool entries only",
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantHelperOK: false,
		},
		{
			name: "additional typescript globs include helper",
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "additionalTypeScriptGlobs": ["lib/**/*.ts"],
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantHelperOK: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "toolbox.pkg.json"), tt.manifest)
			mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), "export default function tool() { return \"ok\"; }\n")
			mustWriteFile(t, filepath.Join(dir, "lib", "internal.ts"), "export const hidden = 1;\n")
			mustWriteFile(t, filepath.Join(dir, ".tooling", "ignored.ts"), "export const ignored = 1;\n")

			pkg, err := LoadSourcePackage(dir)
			if err != nil {
				t.Fatalf("LoadSourcePackage() error = %v", err)
			}

			if _, err := fs.ReadFile(pkg.Files, "tools/calc.add.ts"); err != nil {
				t.Fatalf("expected tool entry to be visible: %v", err)
			}

			_, helperErr := fs.ReadFile(pkg.Files, "lib/internal.ts")
			if tt.wantHelperOK && helperErr != nil {
				t.Fatalf("expected helper file to be visible: %v", helperErr)
			}
			if !tt.wantHelperOK && helperErr == nil {
				t.Fatalf("expected helper file to be hidden")
			}

			if _, err := fs.ReadFile(pkg.Files, ".tooling/ignored.ts"); err == nil {
				t.Fatalf("expected hidden tooling file to be outside package")
			}
		})
	}
}

func TestLoadSourcePackageExtractsToolMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "toolbox.pkg.json"), `{
  "name": "test-pkg",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "users.list.ts"), `/**
 * List all users in the workspace
 */
export default async function tool(params: { query: string; limit?: number }, ctx: unknown) {
  return [];
}
`)

	pkg, err := LoadSourcePackage(dir)
	if err != nil {
		t.Fatalf("LoadSourcePackage() error = %v", err)
	}

	if len(pkg.Package.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(pkg.Package.Tools))
	}
	tool := pkg.Package.Tools[0]
	if tool.Description != "List all users in the workspace" {
		t.Fatalf("expected description %q, got %q", "List all users in the workspace", tool.Description)
	}
	if tool.ParamsSchema == nil {
		t.Fatal("expected params schema to be populated")
	}
	schemaType, _ := tool.ParamsSchema["type"].(string)
	if schemaType != "object" {
		t.Fatalf("expected object schema type, got %q", schemaType)
	}
	props, _ := tool.ParamsSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("expected properties in params schema")
	}
	if _, ok := props["query"]; !ok {
		t.Fatal("expected 'query' property in params schema")
	}

	resolved := pkg.ResolvedTools()
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved tool, got %d", len(resolved))
	}
	if resolved[0].Description != "List all users in the workspace" {
		t.Fatalf("expected resolved description %q, got %q", "List all users in the workspace", resolved[0].Description)
	}
	if resolved[0].ParamsSchema == nil {
		t.Fatal("expected resolved params schema")
	}
}

func TestLoadSourcePackageFallsBackToFilenameDescription(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "toolbox.pkg.json"), `{
  "name": "test-pkg",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), `export default async function tool(params: { a: number; b: number }, ctx: unknown) {
  return params.a + params.b;
}
`)

	pkg, err := LoadSourcePackage(dir)
	if err != nil {
		t.Fatalf("LoadSourcePackage() error = %v", err)
	}

	// No JSDoc, so Description on PackageTool stays empty
	tool := pkg.Package.Tools[0]
	if tool.Description != "" {
		t.Fatalf("expected empty description on PackageTool, got %q", tool.Description)
	}
	// But params schema should still be extracted
	if tool.ParamsSchema == nil {
		t.Fatal("expected params schema even without JSDoc")
	}

	// ResolvedTool should fall back to filename-inferred description
	resolved := pkg.ResolvedTools()
	if resolved[0].Description != "calc.add" {
		t.Fatalf("expected fallback description %q, got %q", "calc.add", resolved[0].Description)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
