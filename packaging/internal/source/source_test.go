package source

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestLoadDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		manifest    string
		wantPackage tooldef.Package
		wantErr     string
	}{
		{
			name: "valid dev package",
			manifest: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name: "wasix runtime with executables",
			manifest: `{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:        "google-workspace",
				Runtime:     tooldef.RuntimeTypeScriptWasixSandbox,
				Executables: map[string]string{"gwc": "dist/gwc.wasm"},
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name:    "missing manifest file",
			wantErr: "no such file",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.manifest != "" {
				mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), tt.manifest)
			}

			loaded, err := LoadDir(dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("LoadDir() error: %v", err)
			}
			if diff := cmp.Diff(tt.wantPackage, loaded.Package); diff != "" {
				t.Fatalf("LoadDir() package mismatch (-want +got):\n%s", diff)
			}
			if loaded.Dir != dir {
				t.Fatalf("expected Dir=%q, got %q", dir, loaded.Dir)
			}
			if loaded.Files == nil {
				t.Fatalf("expected non-nil Files")
			}
		})
	}
}

func TestLoadDirWithMode(t *testing.T) {
	t.Parallel()

	t.Run("dev mode no warning on missing idempotent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "effect": "readOnly" }
  ]
}`)
		result, err := LoadDirWithMode(dir, manifest.ValidationModeDev)
		if err != nil {
			t.Fatalf("LoadDirWithMode() error: %v", err)
		}
		if len(result.Warnings) != 0 {
			t.Fatalf("expected 0 warnings, got %d: %v", len(result.Warnings), result.Warnings)
		}
	})

	t.Run("dist mode accepts missing idempotent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "effect": "reversible" }
  ]
}`)
		_, err := LoadDirWithMode(dir, manifest.ValidationModeDist)
		if err != nil {
			t.Fatalf("expected no error for missing idempotent, got: %v", err)
		}
	})
}

func TestLoadDirExecutables(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)

	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error: %v", err)
	}
	if loaded.Package.Executables == nil {
		t.Fatalf("expected non-nil Executables")
	}
	if got := loaded.Package.Executables["gwc"]; got != "dist/gwc.wasm" {
		t.Fatalf("expected executable gwc=dist/gwc.wasm, got %q", got)
	}
}

func TestSourceFSFiltersTypeScript(t *testing.T) {
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
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
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
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
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
			mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), tt.manifest)
			mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), "export default function tool() { return \"ok\"; }\n")
			mustWriteFile(t, filepath.Join(dir, "lib", "internal.ts"), "export const hidden = 1;\n")
			mustWriteFile(t, filepath.Join(dir, ".tooling", "ignored.ts"), "export const ignored = 1;\n")

			pkg, err := LoadDir(dir)
			if err != nil {
				t.Fatalf("LoadDir() error: %v", err)
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

func TestResolvedTools(t *testing.T) {
	t.Parallel()

	t.Run("typescript sandbox tools", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" },
    { "entry_ts": "tools/calc.sub.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
		mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), "export default function() {}")
		mustWriteFile(t, filepath.Join(dir, "tools", "calc.sub.ts"), "export default function() {}")

		loaded, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir() error: %v", err)
		}

		resolved := loaded.ResolvedTools()
		if len(resolved) != 2 {
			t.Fatalf("expected 2 resolved tools, got %d", len(resolved))
		}

		if resolved[0].Name != "calc.add" {
			t.Fatalf("expected first tool name calc.add, got %q", resolved[0].Name)
		}
		if resolved[0].TS == nil {
			t.Fatalf("expected TS definition for calc.add")
		}
		if resolved[0].TSWasm != nil {
			t.Fatalf("expected no TSWasm for typescript-sandbox tool")
		}
	})

	t.Run("wasix sandbox tools", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
		mustWriteFile(t, filepath.Join(dir, "tools", "users.list.ts"), "export default function() {}")

		loaded, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir() error: %v", err)
		}

		resolved := loaded.ResolvedTools()
		if len(resolved) != 1 {
			t.Fatalf("expected 1 resolved tool, got %d", len(resolved))
		}

		if resolved[0].TSWasm == nil {
			t.Fatalf("expected TSWasm definition for wasix tool")
		}
		if resolved[0].TSWasm.Executables["gwc"] != "dist/gwc.wasm" {
			t.Fatalf("expected executable gwc=dist/gwc.wasm, got %q", resolved[0].TSWasm.Executables["gwc"])
		}
	})
}

func TestResolvedToolsPreservePackageIdentity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "inject": {
        "hosts": ["api.github.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "issues.list.ts"), "export default function() {}")

	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir() error: %v", err)
	}
	if got := loaded.Package.Module; got != tooldef.ModulePath("github.com/example/github-tools") {
		t.Fatalf("loaded module = %q, want %q", got, "github.com/example/github-tools")
	}
	if len(loaded.Package.Credentials) != 1 {
		t.Fatalf("loaded credentials = %d, want 1", len(loaded.Package.Credentials))
	}

	resolved := loaded.ResolvedTools()
	if len(resolved) != 1 {
		t.Fatalf("resolved tools = %d, want 1", len(resolved))
	}
	if resolved[0].Package == nil {
		t.Fatal("resolved tool missing package metadata")
	}
	if got := resolved[0].Package.Module; got != tooldef.ModulePath("github.com/example/github-tools") {
		t.Fatalf("resolved package module = %q, want %q", got, "github.com/example/github-tools")
	}
	if len(resolved[0].Package.Credentials) != 1 {
		t.Fatalf("resolved package credentials = %d, want 1", len(resolved[0].Package.Credentials))
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
