package manifest

import (
	"encoding/json"
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
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts" }
  ]
}`,
			want: DevManifest{
				Module:  testModule("calc"),
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
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "additionalTypeScriptGlobs": ["lib/**/*.ts"],
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: DevManifest{
				Module:                    testModule("calc"),
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
		},
		{
			name: "wasix runtime with executables",
			json: `{
  "module": "example.com/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: DevManifest{
				Module:      testModule("google-workspace"),
				Name:        "google-workspace",
				Runtime:     tooldef.RuntimeTypeScriptWasixSandbox,
				Executables: map[string]string{"gwc": "dist/gwc.wasm"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
		},
		{
			name: "typescript runtime rejects executables",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			wantErr: "not:",
		},
		{
			name: "missing module",
			json: `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: `"module"`,
		},
		{
			name: "missing name",
			json: `{
  "module": "example.com/calc",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: `"name"`,
		},
		{
			name: "missing runtime",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: `"runtime"`,
		},
		{
			name: "manifest with credentials and allowed_hosts",
			json: `{
  "module": "example.com/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ],
  "credentials": [
    {
      "name": "default",
      "type": "oauth2",
      "provider": "google",
      "scopes": ["https://www.googleapis.com/auth/admin.directory.user.readonly"],
      "inject": {
        "hosts": ["*.googleapis.com"],
        "method": "bearer_header",
        "path_prefix": "/admin/directory/v1/"
      }
    },
    {
      "name": "api",
      "type": "api_key",
      "inject": {
        "hosts": ["api.example.com"],
        "method": "api_key_header"
      }
    }
  ],
  "allowed_hosts": ["*.googleapis.com", "api.example.com"]
}`,
			want: DevManifest{
				Module:  testModule("google-workspace"),
				Name:    "google-workspace",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
				Credentials: []DevManifestCredential{
					{
						Name:     "default",
						Type:     "oauth2",
						Provider: json.RawMessage(`"google"`),
						Scopes:   []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
						Inject: DevManifestInject{
							Hosts:      []string{"*.googleapis.com"},
							Method:     "bearer_header",
							PathPrefix: "/admin/directory/v1/",
						},
					},
					{
						Name: "api",
						Type: "api_key",
						Inject: DevManifestInject{
							Hosts:  []string{"api.example.com"},
							Method: "api_key_header",
						},
					},
				},
				AllowedHosts: []string{"*.googleapis.com", "api.example.com"},
			},
		},
		{
			name: "api_key_header with custom header_name",
			json: `{
  "module": "example.com/custom-header",
  "name": "custom-header",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }],
  "credentials": [{
    "name": "default",
    "type": "api_key",
    "inject": {
      "hosts": ["api.example.com"],
      "method": "api_key_header",
      "header_name": "Api-Key"
    }
  }]
}`,
			want: DevManifest{
				Module:  testModule("custom-header"),
				Name:    "custom-header",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts"},
				},
				Credentials: []DevManifestCredential{
					{
						Name: "default",
						Type: "api_key",
						Inject: DevManifestInject{
							Hosts:      []string{"api.example.com"},
							Method:     "api_key_header",
							HeaderName: "Api-Key",
						},
					},
				},
			},
		},
		{
			name: "oauth2 provider object with pkce flag",
			json: `{
  "module": "example.com/custom-oauth",
  "name": "custom-oauth",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/custom.list.ts" }],
  "credentials": [{
    "name": "default",
    "type": "oauth2",
    "provider": {"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token","pkce":false},
    "inject": {
      "hosts": ["api.example.com"],
      "method": "bearer_header"
    }
  }]
}`,
			want: DevManifest{
				Module:  testModule("custom-oauth"),
				Name:    "custom-oauth",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/custom.list.ts"},
				},
				Credentials: []DevManifestCredential{
					{
						Name:     "default",
						Type:     "oauth2",
						Provider: json.RawMessage(`{"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token","pkce":false}`),
						Inject: DevManifestInject{
							Hosts:  []string{"api.example.com"},
							Method: "bearer_header",
						},
					},
				},
			},
		},
		{
			name: "invalid credential type rejected",
			json: `{
  "module": "example.com/bad",
  "name": "bad",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }],
  "credentials": [{
    "name": "default",
    "type": "banana",
    "inject": { "hosts": ["api.example.com"], "method": "bearer_header" }
  }]
}`,
			wantErr: "type",
		},
		{
			name: "invalid injection method rejected",
			json: `{
  "module": "example.com/bad",
  "name": "bad",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }],
  "credentials": [{
    "name": "default",
    "type": "oauth2",
    "inject": { "hosts": ["api.example.com"], "method": "cookie" }
  }]
}`,
			wantErr: "method",
		},
		{
			name: "unknown credential field rejected",
			json: `{
  "module": "example.com/bad",
  "name": "bad",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }],
  "credentials": [{
    "name": "x",
    "type": "oauth2",
    "typo_field": true,
    "inject": { "hosts": ["a.com"], "method": "bearer_header" }
  }]
}`,
			wantErr: "typo_field",
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
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name: "infers effect from verb add",
			dev: DevManifest{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			want: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReversible},
				},
			},
		},
		{
			name: "preserves additionalTypeScriptGlobs",
			dev: DevManifest{
				Module:                    testModule("calc"),
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
				Module:                    testModule("calc"),
				Name:                      "calc",
				Runtime:                   tooldef.RuntimeTypeScriptSandbox,
				AdditionalTypeScriptGlobs: []string{"lib/**/*.ts"},
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
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
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
			mode: ValidationModeDev,
		},
		{
			name: "valid dist complete",
			pkg: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
			mode: ValidationModeDist,
		},
		{
			name: "missing idempotent valid in dev",
			pkg: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Effect: tooldef.EffectReadOnly},
				},
			},
			mode: ValidationModeDev,
		},
		{
			name: "missing idempotent valid in dist",
			pkg: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Effect: tooldef.EffectReversible},
				},
			},
			mode: ValidationModeDist,
		},
		{
			name: "missing effect warns in dev",
			pkg: tooldef.Package{
				Module:  testModule("calc"),
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
			name: "missing effect errors in dist",
			pkg: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			mode:    ValidationModeDist,
			wantErr: `"effect"`,
		},
		{
			name: "package with credentials passes dev validation",
			pkg: tooldef.Package{
				Module:  testModule("google-workspace"),
				Name:    "google-workspace",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
				Credentials: []tooldef.PackageCredential{
					{
						Name: "default",
						Type: "oauth2",
						Provider: &tooldef.OAuth2ProviderConfig{
							AuthURL:  "https://accounts.google.com/o/oauth2/auth",
							TokenURL: "https://oauth2.googleapis.com/token",
						},
						Scopes: []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
						Inject: tooldef.PackageInject{
							Hosts:      []string{"*.googleapis.com"},
							Method:     "bearer_header",
							PathPrefix: "/admin/directory/v1/",
						},
					},
				},
				AllowedHosts: []string{"*.googleapis.com"},
			},
			mode: ValidationModeDev,
		},
		{
			name: "package with credentials passes dist validation",
			pkg: tooldef.Package{
				Module:  testModule("google-workspace"),
				Name:    "google-workspace",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
				Credentials: []tooldef.PackageCredential{
					{
						Name: "default",
						Type: "bearer",
						Inject: tooldef.PackageInject{
							Hosts:  []string{"api.example.com"},
							Method: "bearer_header",
						},
					},
				},
				AllowedHosts: []string{"api.example.com"},
			},
			mode: ValidationModeDist,
		},
		{
			name: "invalid module errors",
			pkg: tooldef.Package{
				Module:  tooldef.ModulePath("not-a-module"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
			mode:    ValidationModeDev,
			wantErr: `invalid module`,
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

func TestInferEffect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entryTS    string
		wantEffect tooldef.Effect
	}{
		{"tools/users.list.ts", tooldef.EffectReadOnly},
		{"tools/users.get.ts", tooldef.EffectReadOnly},
		{"tools/data.read.ts", tooldef.EffectReadOnly},
		{"tools/data.fetch.ts", tooldef.EffectReadOnly},
		{"tools/data.search.ts", tooldef.EffectReadOnly},
		{"tools/data.find.ts", tooldef.EffectReadOnly},
		{"tools/data.describe.ts", tooldef.EffectReadOnly},
		{"tools/users.create.ts", tooldef.EffectReversible},
		{"tools/users.add.ts", tooldef.EffectReversible},
		{"tools/msg.send.ts", tooldef.EffectIrreversible},
		{"tools/msg.post.ts", tooldef.EffectIrreversible},
		{"tools/repo.clone.ts", tooldef.EffectReversible},
		{"tools/item.new.ts", tooldef.EffectReversible},
		{"tools/users.update.ts", tooldef.EffectIrreversible},
		{"tools/users.delete.ts", tooldef.EffectIrreversible},
		{"tools/users.remove.ts", tooldef.EffectIrreversible},
		{"tools/config.set.ts", tooldef.EffectIrreversible},
		{"tools/data.put.ts", tooldef.EffectIrreversible},
		{"tools/data.patch.ts", tooldef.EffectIrreversible},
		{"tools/data.replace.ts", tooldef.EffectIrreversible},
		{"tools/data.edit.ts", tooldef.EffectIrreversible},
		// unknown verb defaults to irreversible
		{"tools/data.sync.ts", tooldef.EffectIrreversible},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.entryTS, func(t *testing.T) {
			t.Parallel()

			got := InferEffect(tt.entryTS)
			if got != tt.wantEffect {
				t.Fatalf("InferEffect(%q) = %q, want %q", tt.entryTS, got, tt.wantEffect)
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
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name: "with sha256 field",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "sha256": "abc123",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				SHA256:  "abc123",
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name: "round-trip with credentials",
			json: `{
  "module": "example.com/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ],
  "credentials": [
    {
      "name": "default",
      "type": "oauth2",
      "provider": { "name": "google" },
      "scopes": ["https://www.googleapis.com/auth/admin.directory.user.readonly"],
      "inject": {
        "hosts": ["*.googleapis.com"],
        "method": "bearer_header",
        "path_prefix": "/admin/directory/v1/"
      }
    }
  ],
  "allowed_hosts": ["*.googleapis.com"]
}`,
			want: tooldef.Package{
				Module:  testModule("google-workspace"),
				Name:    "google-workspace",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/users.list.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
				Credentials: []tooldef.PackageCredential{
					{
						Name:     "default",
						Type:     "oauth2",
						Provider: &tooldef.OAuth2ProviderConfig{Name: "google"},
						Scopes:   []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
						Inject: tooldef.PackageInject{
							Hosts:      []string{"*.googleapis.com"},
							Method:     "bearer_header",
							PathPrefix: "/admin/directory/v1/",
						},
					},
				},
				AllowedHosts: []string{"*.googleapis.com"},
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
				Effect:     effectPtr(tooldef.EffectReadOnly),
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

func TestCompileWithCredentials(t *testing.T) {
	t.Parallel()

	dev := DevManifest{
		Name:    "google-workspace",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Tools: []DevManifestTool{
			{EntryTS: "tools/users.list.ts"},
		},
		Credentials: []DevManifestCredential{
			{
				Name:     "default",
				Type:     "oauth2",
				Provider: json.RawMessage(`"google"`),
				Scopes:   []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
				Inject: DevManifestInject{
					Hosts:                    []string{"*.googleapis.com"},
					Method:                   "bearer_header",
					PathPrefix:               "/admin/directory/v1/",
					AllowUnsafeHTTPInjection: true,
				},
			},
		},
		AllowedHosts: []string{"*.googleapis.com"},
	}

	pkg := Compile(dev)

	if len(pkg.Credentials) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(pkg.Credentials))
	}
	cred := pkg.Credentials[0]
	if cred.Name != "default" {
		t.Fatalf("credential name = %q, want %q", cred.Name, "default")
	}
	if cred.Type != "oauth2" {
		t.Fatalf("credential type = %q, want %q", cred.Type, "oauth2")
	}
	if cred.Provider == nil || cred.Provider.Name != "google" {
		t.Fatalf("credential provider = %+v, want name=google", cred.Provider)
	}
	if len(cred.Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(cred.Scopes))
	}
	if cred.Inject.Method != "bearer_header" {
		t.Fatalf("inject method = %q, want %q", cred.Inject.Method, "bearer_header")
	}
	if cred.Inject.PathPrefix != "/admin/directory/v1/" {
		t.Fatalf("inject path_prefix = %q, want %q", cred.Inject.PathPrefix, "/admin/directory/v1/")
	}
	if !cred.Inject.AllowUnsafeHTTPInjection {
		t.Fatal("inject allow_unsafe_http_injection = false, want true")
	}
	if len(pkg.AllowedHosts) != 1 || pkg.AllowedHosts[0] != "*.googleapis.com" {
		t.Fatalf("allowed_hosts = %v, want [*.googleapis.com]", pkg.AllowedHosts)
	}
}

func TestCompileWithCustomProviderObject(t *testing.T) {
	t.Parallel()

	dev := DevManifest{
		Name:    "custom",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Tools: []DevManifestTool{
			{EntryTS: "tools/custom.list.ts"},
		},
		Credentials: []DevManifestCredential{
			{
				Name:     "default",
				Type:     "oauth2",
				Provider: json.RawMessage(`{"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token","pkce":false}`),
				Inject: DevManifestInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			},
		},
	}

	pkg := Compile(dev)

	if len(pkg.Credentials) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(pkg.Credentials))
	}
	cred := pkg.Credentials[0]
	if cred.Provider == nil {
		t.Fatal("expected non-nil provider")
	}
	if cred.Provider.AuthURL != "https://auth.example.com/authorize" {
		t.Fatalf("provider auth_url = %q, want https://auth.example.com/authorize", cred.Provider.AuthURL)
	}
	if cred.Provider.TokenURL != "https://auth.example.com/token" {
		t.Fatalf("provider token_url = %q, want https://auth.example.com/token", cred.Provider.TokenURL)
	}
	if cred.Provider.PKCE == nil || *cred.Provider.PKCE {
		t.Fatalf("provider pkce = %+v, want false", cred.Provider.PKCE)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func effectPtr(v tooldef.Effect) *tooldef.Effect {
	return &v
}

func testModule(name string) tooldef.ModulePath {
	return tooldef.ModulePath("example.com/" + name)
}
