package manifest

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestParseDev(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		want    DevManifest
		wantErr string
	}{
		{
			name: "minimal valid",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			want: DevManifest{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts"},
				},
			},
		},
		{
			name: "full manifest",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "additionalTypeScriptGlobs": ["lib/**/*.ts"],
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			want: DevManifest{
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: accessModePtr(tooldef.AccessModeReadOnly)},
				},
			},
		},
		{
			name: "wasix runtime with executables",
			json: `{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			want: DevManifest{
				Name:        "google-workspace",
				Runtime:     tooldef.RuntimeTypeScriptWasixSandbox,
				Executables: map[string]string{"gwc": "dist/gwc.wasm"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), AccessMode: accessModePtr(tooldef.AccessModeReadOnly)},
				},
			},
		},
		{
			name: "typescript runtime rejects executables",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			wantErr: "not:",
		},
		{
			name: "missing name",
			json: `{
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: `"name"`,
		},
		{
			name: "missing runtime",
			json: `{
  "name": "calc",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: `"runtime"`,
		},
		{
			name:    "invalid json",
			json:    `{not json`,
			wantErr: "invalid character",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDev([]byte(tt.json))
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
				t.Fatalf("ParseDev() error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("ParseDev() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCompile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dev  DevManifest
		want tooldef.Package
	}{
		{
			name: "basic compilation",
			dev: DevManifest{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: accessModePtr(tooldef.AccessModeReadOnly)},
				},
			},
			want: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "infers accessMode from verb add",
			dev: DevManifest{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			want: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReversible},
				},
			},
		},
		{
			name: "preserves additionalTypeScriptGlobs",
			dev: DevManifest{
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: accessModePtr(tooldef.AccessModeReadOnly)},
				},
			},
			want: tooldef.Package{
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Compile(tt.dev)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("Compile() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateCompiled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pkg          tooldef.Package
		mode         ValidationMode
		wantWarnings int
		wantErr      string
	}{
		{
			name: "valid dev complete",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
			mode: ValidationModeDev,
		},
		{
			name: "valid dist complete",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
			mode: ValidationModeDist,
		},
		{
			name: "missing idempotent valid in dev",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", AccessMode: tooldef.AccessModeReadOnly},
				},
			},
			mode: ValidationModeDev,
		},
		{
			name: "missing idempotent valid in dist",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", AccessMode: tooldef.AccessModeReversible},
				},
			},
			mode: ValidationModeDist,
		},
		{
			name: "missing accessMode warns in dev",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			mode:         ValidationModeDev,
			wantWarnings: 1,
		},
		{
			name: "missing accessMode errors in dist",
			pkg: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			mode:    ValidationModeDist,
			wantErr: `"accessMode"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			warnings, err := ValidateCompiled(tt.pkg, tt.mode)
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
				t.Fatalf("ValidateCompiled() error: %v", err)
			}
			if len(warnings) != tt.wantWarnings {
				t.Fatalf("expected %d warnings, got %d: %v", tt.wantWarnings, len(warnings), warnings)
			}
		})
	}
}

func TestInferAccessMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entryTS  string
		wantMode tooldef.AccessMode
	}{
		{"tools/users.list.ts", tooldef.AccessModeReadOnly},
		{"tools/users.get.ts", tooldef.AccessModeReadOnly},
		{"tools/data.read.ts", tooldef.AccessModeReadOnly},
		{"tools/data.fetch.ts", tooldef.AccessModeReadOnly},
		{"tools/data.search.ts", tooldef.AccessModeReadOnly},
		{"tools/data.find.ts", tooldef.AccessModeReadOnly},
		{"tools/data.describe.ts", tooldef.AccessModeReadOnly},
		{"tools/users.create.ts", tooldef.AccessModeReversible},
		{"tools/users.add.ts", tooldef.AccessModeReversible},
		{"tools/msg.send.ts", tooldef.AccessModeReversible},
		{"tools/msg.post.ts", tooldef.AccessModeReversible},
		{"tools/repo.clone.ts", tooldef.AccessModeReversible},
		{"tools/item.new.ts", tooldef.AccessModeReversible},
		{"tools/users.update.ts", tooldef.AccessModeIrreversible},
		{"tools/users.delete.ts", tooldef.AccessModeIrreversible},
		{"tools/users.remove.ts", tooldef.AccessModeIrreversible},
		{"tools/config.set.ts", tooldef.AccessModeIrreversible},
		{"tools/data.put.ts", tooldef.AccessModeIrreversible},
		{"tools/data.patch.ts", tooldef.AccessModeIrreversible},
		{"tools/data.replace.ts", tooldef.AccessModeIrreversible},
		{"tools/data.edit.ts", tooldef.AccessModeIrreversible},
		// unknown verb defaults to irreversible
		{"tools/data.sync.ts", tooldef.AccessModeIrreversible},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.entryTS, func(t *testing.T) {
			t.Parallel()

			got := InferAccessMode(tt.entryTS)
			if got != tt.wantMode {
				t.Fatalf("InferAccessMode(%q) = %q, want %q", tt.entryTS, got, tt.wantMode)
			}
		})
	}
}

func TestInferToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entryTS string
		want    string
	}{
		{"tools/calc.add.ts", "calc.add"},
		{"tools/users.list.ts", "users.list"},
		{"tools/users.calendars.events.list.ts", "users.calendars.events.list"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.entryTS, func(t *testing.T) {
			t.Parallel()

			got := InferToolName(tt.entryTS)
			if got != tt.want {
				t.Fatalf("InferToolName(%q) = %q, want %q", tt.entryTS, got, tt.want)
			}
		})
	}
}

func TestParsePkg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		want    tooldef.Package
		wantErr string
	}{
		{
			name: "valid compiled package",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name: "with sha256 field",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "sha256": "abc123",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				SHA256:  "abc123",
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), AccessMode: tooldef.AccessModeReadOnly},
				},
			},
		},
		{
			name:    "invalid json",
			json:    `{bad}`,
			wantErr: "invalid character",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePkg([]byte(tt.json))
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
				t.Fatalf("ParsePkg() error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("ParsePkg() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInferResourceParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entryTS string
		want    []ResourceParam
	}{
		// Single resource: account.tickets.list -> account_id (list doesn't need ticket_id)
		{"tools/account.tickets.list.ts", []ResourceParam{
			{Name: "account_id", BindingName: "account_id"},
		}},
		// Single resource: account.tickets.get -> account_id, ticket_id (get needs deepest)
		{"tools/account.tickets.get.ts", []ResourceParam{
			{Name: "account_id", BindingName: "account_id"},
			{Name: "ticket_id", BindingName: "ticket_id"},
		}},
		// Deep nesting: users.calendars.events.list -> user_id, calendar_id
		{"tools/users.calendars.events.list.ts", []ResourceParam{
			{Name: "user_id", BindingName: "user_id"},
			{Name: "calendar_id", BindingName: "calendar_id"},
		}},
		// Deep nesting with get: users.calendars.events.get -> user_id, calendar_id, event_id
		{"tools/users.calendars.events.get.ts", []ResourceParam{
			{Name: "user_id", BindingName: "user_id"},
			{Name: "calendar_id", BindingName: "calendar_id"},
			{Name: "event_id", BindingName: "event_id"},
		}},
		// Unknown verb defaults to member (includes deepest ID)
		{"tools/account.tickets.archive.ts", []ResourceParam{
			{Name: "account_id", BindingName: "account_id"},
			{Name: "ticket_id", BindingName: "ticket_id"},
		}},
		// Flat tool: calc.add -> no resource params
		{"tools/calc.add.ts", nil},
		// Simple tool: users.list -> no parent resources (list at top level)
		{"tools/users.list.ts", nil},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.entryTS, func(t *testing.T) {
			t.Parallel()

			got := InferResourceParams(tt.entryTS)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("InferResourceParams(%q) mismatch (-want +got):\n%s", tt.entryTS, diff)
			}
		})
	}
}

func TestCompileWithResourceBindingsOverride(t *testing.T) {
	t.Parallel()

	dev := DevManifest{
		Name:    "zendesk",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Tools: []DevManifestTool{
			{
				EntryTS:    "tools/account.tickets.list.ts",
				Idempotent: boolPtr(true),
				AccessMode: accessModePtr(tooldef.AccessModeReadOnly),
				Resource: &DevManifestToolResource{
					Bindings: map[string]string{"account_id": "zendesk_account"},
				},
			},
		},
	}

	pkg := Compile(dev)

	if len(pkg.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(pkg.Tools))
	}

	tool := pkg.Tools[0]
	if len(tool.ResourceParams) != 1 {
		t.Fatalf("expected 1 resource param, got %d", len(tool.ResourceParams))
	}

	rp := tool.ResourceParams[0]
	if rp.Name != "account_id" {
		t.Fatalf("expected resource param name 'account_id', got %q", rp.Name)
	}
	if rp.BindingName != "zendesk_account" {
		t.Fatalf("expected binding name 'zendesk_account', got %q", rp.BindingName)
	}
}

func TestCompileWithResourceModeOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		entryTS        string
		mode           string
		wantParamNames []string
	}{
		{
			// "archive" is normally a member verb (includes deepest ID: ticket_id).
			// Forcing "collection" mode should exclude the deepest ID.
			name:           "archive forced collection excludes deepest ID",
			entryTS:        "tools/account.tickets.archive.ts",
			mode:           "collection",
			wantParamNames: []string{"account_id"},
		},
		{
			// "list" is normally a collection verb (excludes deepest ID).
			// Forcing "member" mode should include the deepest ID.
			name:           "list forced member includes deepest ID",
			entryTS:        "tools/account.tickets.list.ts",
			mode:           "member",
			wantParamNames: []string{"account_id", "ticket_id"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dev := DevManifest{
				Name:    "test",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{
						EntryTS: tt.entryTS,
						Resource: &DevManifestToolResource{
							Mode: tt.mode,
						},
					},
				},
			}

			pkg := Compile(dev)

			if len(pkg.Tools) != 1 {
				t.Fatalf("expected 1 tool, got %d", len(pkg.Tools))
			}

			tool := pkg.Tools[0]
			if len(tool.ResourceParams) != len(tt.wantParamNames) {
				t.Fatalf("expected %d resource params, got %d: %v", len(tt.wantParamNames), len(tool.ResourceParams), tool.ResourceParams)
			}

			for i, wantName := range tt.wantParamNames {
				if tool.ResourceParams[i].Name != wantName {
					t.Fatalf("param[%d]: expected name %q, got %q", i, wantName, tool.ResourceParams[i].Name)
				}
			}
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func accessModePtr(v tooldef.AccessMode) *tooldef.AccessMode {
	return &v
}
