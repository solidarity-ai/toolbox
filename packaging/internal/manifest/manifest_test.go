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
  "useWhenHint": "Use when you need calculator-style arithmetic tools.",
  "runtime": "typescript-sandbox",
  "additionalTypeScriptGlobs": ["lib/**/*.ts"],
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: DevManifest{
				Module:                    testModule("calc"),
				Name:                      "calc",
				UseWhenHint:               "Use when you need calculator-style arithmetic tools.",
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
      "instructions": "Create OAuth client credentials in Google Cloud Console.",
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
						Name:         "default",
						Type:         "oauth2",
						Instructions: "Create OAuth client credentials in Google Cloud Console.",
						Provider:     json.RawMessage(`"google"`),
						Scopes:       []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
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
			name: "useWhenHint too long rejected",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "useWhenHint": "` + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` + `",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: "useWhenHint",
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
			name: "pack-managed field rejected",
			json: `{
  "module": "example.com/bad",
  "name": "bad",
  "runtime": "typescript-sandbox",
  "packedByToolboxVersion": "v1.0.0",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`,
			wantErr: "packedByToolboxVersion",
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
				Module:      testModule("calc"),
				Name:        "calc",
				UseWhenHint: "Use when you need calculator-style arithmetic tools.",
				Runtime:     tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
				Module:      testModule("calc"),
				Name:        "calc",
				UseWhenHint: "Use when you need calculator-style arithmetic tools.",
				Runtime:     tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
		},
		{
			name: "preserves minimum Toolbox version",
			dev: DevManifest{
				MinimumToolboxVersion: "v0.9.0",
				Module:                testModule("calc"),
				Name:                  "calc",
				Runtime:               tooldef.RuntimeTypeScriptSandbox,
				Tools: []DevManifestTool{
					{EntryTS: "tools/calc.add.ts", Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
				MinimumToolboxVersion: "v0.9.0",
				Module:                testModule("calc"),
				Name:                  "calc",
				Runtime:               tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Effect: tooldef.EffectReadOnly},
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
			pkg: distPackage(tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			}),
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
			pkg: distPackage(tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Effect: tooldef.EffectReversible},
				},
			}),
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
			pkg: distPackage(tooldef.Package{
				Module:  testModule("calc"),
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			}),
			mode:    ValidationModeDist,
			wantErr: `"effect"`,
		},
		{
			name: "useWhenHint too long errors",
			pkg: tooldef.Package{
				Module:      testModule("calc"),
				Name:        "calc",
				UseWhenHint: strings.Repeat("a", 101),
				Runtime:     tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
				},
			},
			mode:    ValidationModeDev,
			wantErr: "useWhenHint",
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
			pkg: distPackage(tooldef.Package{
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
			}),
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
  "useWhenHint": "Use when you need calculator-style arithmetic tools.",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Module:      testModule("calc"),
				Name:        "calc",
				UseWhenHint: "Use when you need calculator-style arithmetic tools.",
				Runtime:     tooldef.RuntimeTypeScriptSandbox,
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
      "instructions": "Create OAuth client credentials in Google Cloud Console.",
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
						Name:         "default",
						Type:         "oauth2",
						Instructions: "Create OAuth client credentials in Google Cloud Console.",
						Provider:     &tooldef.OAuth2ProviderConfig{Name: "google"},
						Scopes:       []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
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
			name: "useWhenHint too long rejected",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "useWhenHint": "` + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` + `",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			wantErr: "useWhenHint",
		},
		{
			name:    "invalid json",
			json:    `{bad}`,
			wantErr: "invalid character",
		},
		{
			name: "unknown field rejected",
			json: `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }],
  "futureField": true
}`,
			wantErr: "unknown field",
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

func distPackage(pkg tooldef.Package) tooldef.Package {
	pkg.ManifestSchemaVersion = tooldef.PackageManifestSchemaVersion
	pkg.MinimumToolboxVersion = "v1.0.0"
	pkg.PackedByToolboxVersion = "v1.0.0"
	return pkg
}

func TestParseDevResourceValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resources string
		wantErr   string
	}{
		{"invalid segment", `[{
  "path":"work_books","params":[{"name":"workbook"}]
}]`, "invalid segment"},
		{"empty params", `[{"path":"workbooks","params":[]}]`, "minItems"},
		{"empty name", `[{"path":"workbooks","params":[{"name":""}]}]`, "properties/name: minLength"},
		{"empty binding", `[{"path":"workbooks","params":[{"name":"workbook","binding_name":""}]}]`, "properties/binding_name: minLength"},
		{"duplicate path", `[
  {"path":"workbooks","params":[{"name":"workbook"}]},
  {"path":"workbooks","params":[{"name":"other"}]}
]`, `duplicate resource path "workbooks"`},
		{"duplicate param", `[{
  "path":"workbooks","params":[{"name":"workbook"},{"name":"workbook"}]
}]`, `duplicate selector parameter "workbook"`},
		{"repeated ancestor param", `[
  {"path":"workbooks","params":[{"name":"workbook"}]},
  {"path":"workbooks.sheets","params":[{"name":"workbook"}]}
]`, `repeats selector parameter "workbook" from ancestor "workbooks"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDev(resourceTestManifest(tt.resources))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseDev() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParsePkgResourceValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resources string
		wantErr   string
	}{
		{"invalid segment", `[{"path":"work-books","params":[{"name":"workbook","binding_name":"workbook"}]}]`, "invalid segment"},
		{"empty params", `[{"path":"workbooks","params":[]}]`, "must declare at least one selector parameter"},
		{"empty name", `[{"path":"workbooks","params":[{"name":"","binding_name":"workbook"}]}]`, "empty selector parameter name or binding name"},
		{"empty binding", `[{"path":"workbooks","params":[{"name":"workbook","binding_name":""}]}]`, "empty selector parameter name or binding name"},
		{"duplicate path", `[
  {"path":"workbooks","params":[{"name":"workbook","binding_name":"workbook"}]},
  {"path":"workbooks","params":[{"name":"other","binding_name":"other"}]}
]`, `duplicate resource path "workbooks"`},
		{"duplicate param", `[{
  "path":"workbooks","params":[
    {"name":"workbook","binding_name":"workbook"},
    {"name":"workbook","binding_name":"other"}
  ]
}]`, `duplicate selector parameter "workbook"`},
		{"repeated ancestor param", `[
  {"path":"workbooks","params":[{"name":"workbook","binding_name":"workbook"}]},
  {"path":"workbooks.sheets","params":[{"name":"workbook","binding_name":"other"}]}
]`, `repeats selector parameter "workbook" from ancestor "workbooks"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParsePkg(resourceTestManifest(tt.resources))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParsePkg() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func resourceTestManifest(resources string) []byte {
	return []byte(`{
  "module":"example.com/resources",
  "name":"resources",
  "runtime":"typescript-sandbox",
  "resources":` + resources + `,
  "tools":[{"entry_ts":"tools/resources.list.ts","effect":"readOnly"}]
}`)
}

func TestCompileWithPackageResources(t *testing.T) {
	t.Parallel()

	dev := DevManifest{
		Name:    "zendesk",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Resources: []DevManifestResource{
			{
				Path: "accounts",
				Params: []DevManifestResourceParam{
					{Name: "account", BindingName: "zendesk_account"},
				},
			},
			{
				Path: "accounts.tickets",
				Params: []DevManifestResourceParam{
					{Name: "start"},
					{Name: "end"},
				},
			},
		},
		Tools: []DevManifestTool{
			{
				EntryTS:    "tools/accounts.tickets.get.ts",
				Idempotent: boolPtr(true),
				Effect:     effectPtr(tooldef.EffectReadOnly),
			},
		},
	}

	pkg := Compile(dev)
	want := []tooldef.Resource{
		{Path: "accounts", Params: []tooldef.ResourceParam{{Name: "account", BindingName: "zendesk_account"}}},
		{Path: "accounts.tickets", Params: []tooldef.ResourceParam{{Name: "start", BindingName: "start"}, {Name: "end", BindingName: "end"}}},
	}
	if diff := cmp.Diff(want, pkg.Resources); diff != "" {
		t.Fatalf("compiled resources mismatch (-want +got):\n%s", diff)
	}
}

func TestParseDevRejectsLegacyPerToolResourceConfig(t *testing.T) {
	t.Parallel()

	_, err := ParseDev([]byte(`{
  "module": "example.com/legacy",
  "name": "legacy",
  "runtime": "typescript-sandbox",
  "tools": [
    {"entry_ts":"tools/workbook.officejs.run.ts","resource":{"mode":"collection"}}
  ]
}`))
	if err == nil || !strings.Contains(err.Error(), "additional properties") {
		t.Fatalf("ParseDev() error = %v, want legacy per-tool resource rejection", err)
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
