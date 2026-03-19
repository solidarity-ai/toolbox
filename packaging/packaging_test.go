package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestLoadPackageFromDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		mode         ValidationMode
		manifest     string
		wantPackage  tooldef.Package
		wantWarnings int
		wantErr      string
	}{
		{
			name: "valid dev",
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
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
		},
		{
			name: "valid dist",
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
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
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
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			wantPackage: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts"},
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
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			wantErr: `"idempotent"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			manifestPath := filepath.Join(dir, "toolbox.pkg.json")
			if err := os.WriteFile(manifestPath, []byte(tt.manifest), 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}

			result, err := LoadPackageFromDirWithMode(dir, tt.mode)
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
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}
