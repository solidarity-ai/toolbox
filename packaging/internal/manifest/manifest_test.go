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
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: DevManifest{
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
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: DevManifest{
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
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
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
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReversible},
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
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: effectPtr(tooldef.EffectReadOnly)},
				},
			},
			want: tooldef.Package{
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
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true)},
				},
			},
			mode:    ValidationModeDist,
			wantErr: `"effect"`,
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
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: tooldef.Package{
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
  "name": "calc",
  "runtime": "typescript-sandbox",
  "sha256": "abc123",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`,
			want: tooldef.Package{
				Name:    "calc",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
				SHA256:  "abc123",
				Tools: []tooldef.PackageTool{
					{EntryTS: "tools/calc.add.ts", Idempotent: boolPtr(true), Effect: tooldef.EffectReadOnly},
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

func TestCompilePreservesModuleAndCredentials(t *testing.T) {
	t.Parallel()

	dev := DevManifest{
		Module:  tooldef.ModulePath("github.com/example/acme-tools"),
		Name:    "acme-tools",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Credentials: []tooldef.PackageCredential{{
			Name:     "github_token",
			Type:     tooldef.CredentialTypeBearer,
			Provider: tooldef.OAuth2ProviderRef{Name: "github"},
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		}},
		Tools: []DevManifestTool{{
			EntryTS:    "tools/issues.list.ts",
			Idempotent: boolPtr(true),
			Effect:     effectPtr(tooldef.EffectReadOnly),
		}},
	}

	got := Compile(dev)
	if got.Module != dev.Module {
		t.Fatalf("Compile() module = %q, want %q", got.Module, dev.Module)
	}
	if diff := cmp.Diff(dev.Credentials, got.Credentials); diff != "" {
		t.Fatalf("Compile() credentials mismatch (-want +got):\n%s", diff)
	}
}

func TestOAuth2ProviderManifestValidation(t *testing.T) {
	t.Parallel()

	t.Run("built-in provider survives compile and load round-trip", func(t *testing.T) {
		t.Parallel()
		dev, err := ParseDev([]byte(`{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "provider": "google",
      "inject": { "hosts": ["www.googleapis.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/users.list.ts" }
  ]
}`))
		if err != nil {
			t.Fatalf("ParseDev() error: %v", err)
		}
		wantProvider := tooldef.OAuth2ProviderRef{Name: "google"}
		if diff := cmp.Diff(wantProvider, dev.Credentials[0].Provider); diff != "" {
			t.Fatalf("ParseDev() provider mismatch (-want +got):\n%s", diff)
		}

		compiled := Compile(dev)
		raw, err := json.Marshal(compiled)
		if err != nil {
			t.Fatalf("json.Marshal(compiled) error: %v", err)
		}
		loaded, err := ParsePkg(raw)
		if err != nil {
			t.Fatalf("ParsePkg() error: %v", err)
		}
		if diff := cmp.Diff(wantProvider, loaded.Credentials[0].Provider); diff != "" {
			t.Fatalf("ParsePkg() provider mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("explicit provider survives compile and load round-trip", func(t *testing.T) {
		t.Parallel()
		dev, err := ParseDev([]byte(`{
  "module": "github.com/example/custom-oauth",
  "name": "custom-oauth",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "custom_service",
      "type": "oauth2",
      "provider": {
        "auth_url": "https://auth.custom.com/oauth/authorize",
        "token_url": "https://auth.custom.com/oauth/token"
      },
      "inject": { "hosts": ["api.custom.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/things.list.ts" }
  ]
}`))
		if err != nil {
			t.Fatalf("ParseDev() error: %v", err)
		}
		wantProvider := explicitProviderRef("https://auth.custom.com/oauth/authorize", "https://auth.custom.com/oauth/token")
		if diff := cmp.Diff(wantProvider, dev.Credentials[0].Provider); diff != "" {
			t.Fatalf("ParseDev() provider mismatch (-want +got):\n%s", diff)
		}

		compiled := Compile(dev)
		raw, err := json.Marshal(compiled)
		if err != nil {
			t.Fatalf("json.Marshal(compiled) error: %v", err)
		}
		loaded, err := ParsePkg(raw)
		if err != nil {
			t.Fatalf("ParsePkg() error: %v", err)
		}
		if diff := cmp.Diff(wantProvider, loaded.Credentials[0].Provider); diff != "" {
			t.Fatalf("ParsePkg() provider mismatch (-want +got):\n%s", diff)
		}
	})

	for _, tt := range []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "rejects missing oauth2 provider",
			json: `{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "inject": { "hosts": ["www.googleapis.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/users.list.ts" }
  ]
}`,
			wantErr: "credentials[0].provider: oauth2 provider is required",
		},
		{
			name: "rejects unknown built-in provider",
			json: `{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "provider": "not-a-provider",
      "inject": { "hosts": ["www.googleapis.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/users.list.ts" }
  ]
}`,
			wantErr: `credentials[0].provider: unknown oauth2 provider "not-a-provider"`,
		},
		{
			name: "rejects malformed explicit provider urls",
			json: `{
  "module": "github.com/example/custom-oauth",
  "name": "custom-oauth",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "custom_service",
      "type": "oauth2",
      "provider": {
        "auth_url": "http://auth.custom.com/oauth/authorize",
        "token_url": "https://auth.custom.com/oauth/token"
      },
      "inject": { "hosts": ["api.custom.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/things.list.ts" }
  ]
}`,
			wantErr: `credentials[0].provider: auth_url "http://auth.custom.com/oauth/authorize" must use https`,
		},
		{
			name: "rejects explicit provider object on non oauth credential",
			json: `{
  "module": "github.com/example/custom-oauth",
  "name": "custom-oauth",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "api_key",
      "type": "bearer",
      "provider": {
        "auth_url": "https://auth.custom.com/oauth/authorize",
        "token_url": "https://auth.custom.com/oauth/token"
      },
      "inject": { "hosts": ["api.custom.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/things.list.ts" }
  ]
}`,
			wantErr: "credentials[0].provider explicit oauth2 endpoints are only allowed when type is oauth2",
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDev([]byte(tt.json))
			if err == nil {
				t.Fatalf("ParseDev() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseDev() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestCredentialMetadataValidation(t *testing.T) {
	t.Parallel()

	t.Run("dev manifest requires module when credentials are declared", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "inject": { "hosts": ["api.github.com"] }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "module is required") {
			t.Fatalf("ParseDev() error = %v, want module requirement", err)
		}
	})

	t.Run("dev manifest rejects secret-valued declaration fields", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "token": "shh",
      "inject": { "hosts": ["api.github.com"] }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "token") {
			t.Fatalf("ParseDev() error = %v, want token field mention", err)
		}
	})

	t.Run("compiled manifest rejects secret-valued declaration fields", func(t *testing.T) {
		t.Parallel()
		_, err := ParsePkg([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "client_secret": "shh",
      "inject": { "hosts": ["api.github.com"] }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts", "effect": "readOnly" }
  ]
}`))
		if err == nil {
			t.Fatal("ParsePkg() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "client_secret") {
			t.Fatalf("ParsePkg() error = %v, want client_secret field mention", err)
		}
	})

	t.Run("compiled validation rejects invalid module and empty hosts", func(t *testing.T) {
		t.Parallel()
		_, err := ValidateCompiled(tooldef.Package{
			Module:  tooldef.ModulePath("not-a-module"),
			Name:    "github-tools",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "github_token",
				Type: tooldef.CredentialTypeBearer,
				Inject: tooldef.CredentialInject{
					Hosts: []string{""},
				},
			}},
			Tools: []tooldef.PackageTool{{
				EntryTS: "tools/issues.list.ts",
				Effect:  tooldef.EffectReadOnly,
			}},
		}, ValidationModeDist)
		if err == nil {
			t.Fatal("ValidateCompiled() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "invalid module") {
			t.Fatalf("ValidateCompiled() error = %v, want invalid module", err)
		}
	})

	t.Run("dev manifest rejects missing api_key header name", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "api_key",
      "type": "api_key",
      "inject": { "hosts": ["api.github.com"], "method": "api_key_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "credentials[0].inject.headerName") {
			t.Fatalf("ParseDev() error = %v, want headerName field mention", err)
		}
	})

	t.Run("dev manifest rejects invalid api_key query name", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "api_key",
      "type": "api_key",
      "inject": {
        "hosts": ["api.github.com"],
        "method": "api_key_query",
        "queryName": "bad=name"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "credentials[0].inject.queryName") {
			t.Fatalf("ParseDev() error = %v, want queryName field mention", err)
		}
	})

	t.Run("dev manifest rejects stray inject metadata on bearer header rules", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "oauth",
      "type": "oauth2",
      "inject": {
        "hosts": ["www.googleapis.com"],
        "method": "bearer_header",
        "headerName": "X-Should-Not-Exist"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "inject.headerName") {
			t.Fatalf("ParseDev() error = %v, want stray headerName field mention", err)
		}
	})

	t.Run("schema rejects unsupported inject method", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "oauth",
      "type": "oauth2",
      "inject": {
        "hosts": ["www.googleapis.com"],
        "method": "digest_auth"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts" }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "method") {
			t.Fatalf("ParseDev() error = %v, want inject method mention", err)
		}
	})
}

func TestManifestAllowedHostsMetadata(t *testing.T) {
	t.Parallel()

	t.Run("parse dev manifest preserves package and tool allowlist fields", func(t *testing.T) {
		t.Parallel()
		got, err := ParseDev([]byte(`{
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["*.googleapis.com", "oauth2.googleapis.com"],
  "tools": [
    { "entry_ts": "tools/users.list.ts" },
    { "entry_ts": "tools/admin.list.ts", "allowed_hosts": ["admin.googleapis.com"] },
    { "entry_ts": "tools/notifications.send.ts", "allowed_hosts_extend": ["hooks.slack.com"] }
  ]
}`))
		if err != nil {
			t.Fatalf("ParseDev() error: %v", err)
		}
		if diff := cmp.Diff([]string{"*.googleapis.com", "oauth2.googleapis.com"}, got.AllowedHosts); diff != "" {
			t.Fatalf("package allowed hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"admin.googleapis.com"}, got.Tools[1].AllowedHosts); diff != "" {
			t.Fatalf("tool allowed_hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"hooks.slack.com"}, got.Tools[2].AllowedHostsExtend); diff != "" {
			t.Fatalf("tool allowed_hosts_extend mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("compile preserves allowlist metadata", func(t *testing.T) {
		t.Parallel()
		pkg := Compile(DevManifest{
			Name:         "google-workspace",
			Runtime:      tooldef.RuntimeTypeScriptSandbox,
			AllowedHosts: []string{"*.googleapis.com", "oauth2.googleapis.com"},
			Tools: []DevManifestTool{
				{EntryTS: "tools/users.list.ts"},
				{EntryTS: "tools/admin.list.ts", AllowedHosts: []string{"admin.googleapis.com"}},
				{EntryTS: "tools/notifications.send.ts", AllowedHostsExtend: []string{"hooks.slack.com"}},
			},
		})
		if diff := cmp.Diff([]string{"*.googleapis.com", "oauth2.googleapis.com"}, pkg.AllowedHosts); diff != "" {
			t.Fatalf("package allowed hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"admin.googleapis.com"}, pkg.Tools[1].AllowedHosts); diff != "" {
			t.Fatalf("tool allowed_hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"hooks.slack.com"}, pkg.Tools[2].AllowedHostsExtend); diff != "" {
			t.Fatalf("tool allowed_hosts_extend mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("parse compiled package preserves allowlist metadata", func(t *testing.T) {
		t.Parallel()
		got, err := ParsePkg([]byte(`{
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["*.googleapis.com"],
  "tools": [
    { "entry_ts": "tools/users.list.ts", "effect": "readOnly" },
    { "entry_ts": "tools/admin.list.ts", "effect": "readOnly", "allowed_hosts": ["admin.googleapis.com"] },
    { "entry_ts": "tools/notifications.send.ts", "effect": "irreversible", "allowed_hosts_extend": ["hooks.slack.com"] }
  ]
}`))
		if err != nil {
			t.Fatalf("ParsePkg() error: %v", err)
		}
		if diff := cmp.Diff([]string{"*.googleapis.com"}, got.AllowedHosts); diff != "" {
			t.Fatalf("package allowed hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"admin.googleapis.com"}, got.Tools[1].AllowedHosts); diff != "" {
			t.Fatalf("tool allowed_hosts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"hooks.slack.com"}, got.Tools[2].AllowedHostsExtend); diff != "" {
			t.Fatalf("tool allowed_hosts_extend mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestManifestAllowedHostsValidation(t *testing.T) {
	t.Parallel()

	t.Run("rejects empty package host entry", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "allowed_hosts": [""],
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "allowed_hosts") {
			t.Fatalf("ParseDev() error = %v, want allowed_hosts field mention", err)
		}
	})

	t.Run("rejects tool replace plus extend combination", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/calc.add.ts",
      "allowed_hosts": ["api.example.com"],
      "allowed_hosts_extend": ["hooks.example.com"]
    }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "allowed_hosts") || !strings.Contains(err.Error(), "allowed_hosts_extend") {
			t.Fatalf("ParseDev() error = %v, want both allowlist fields mentioned", err)
		}
	})

	t.Run("schema rejects wrong allowed_hosts type", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "allowed_hosts": "api.example.com",
  "tools": [{ "entry_ts": "tools/calc.add.ts" }]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "allowed_hosts") {
			t.Fatalf("ParseDev() error = %v, want allowed_hosts field mention", err)
		}
	})
}

func TestToolCredentialOverrideSemantics(t *testing.T) {
	t.Parallel()

	dev, err := ParseDev([]byte(`{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "provider": "google",
      "inject": { "hosts": ["www.googleapis.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/users.list.ts" },
    { "entry_ts": "tools/status.check.ts", "credentials": [] },
    {
      "entry_ts": "tools/calendar.list.ts",
      "credentials": [
        {
          "name": "google_calendar",
          "type": "oauth2",
          "provider": "google",
          "inject": { "hosts": ["www.googleapis.com"], "method": "bearer_header" }
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("ParseDev() error: %v", err)
	}

	if dev.Tools[0].CredentialsPresent {
		t.Fatal("tool[0] credentials should be absent and inherit package credentials")
	}
	if !dev.Tools[1].CredentialsPresent {
		t.Fatal("tool[1] credentials should preserve explicit empty declaration")
	}
	if len(dev.Tools[1].Credentials) != 0 {
		t.Fatalf("tool[1] credentials len = %d, want 0", len(dev.Tools[1].Credentials))
	}
	if !dev.Tools[2].CredentialsPresent {
		t.Fatal("tool[2] credentials should preserve explicit replacement declaration")
	}
	if diff := cmp.Diff([]tooldef.PackageCredential{{
		Name:     "google_calendar",
		Type:     tooldef.CredentialTypeOAuth2,
		Provider: tooldef.OAuth2ProviderRef{Name: "google"},
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"www.googleapis.com"},
			Method: "bearer_header",
		},
	}}, dev.Tools[2].Credentials); diff != "" {
		t.Fatalf("ParseDev() tool replacement credentials mismatch (-want +got):\n%s", diff)
	}

	pkg := Compile(dev)
	if pkg.Tools[0].CredentialsPresent {
		t.Fatal("Compile() should keep absent tool credentials absent")
	}
	if !pkg.Tools[1].CredentialsPresent {
		t.Fatal("Compile() should preserve explicit empty tool credentials")
	}
	if !pkg.Tools[2].CredentialsPresent {
		t.Fatal("Compile() should preserve explicit replacement tool credentials")
	}

	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("json.Marshal(pkg) error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(compiled) error: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 3 {
		t.Fatalf("compiled JSON tools = %#v, want 3 entries", payload["tools"])
	}
	toolJSON, ok := tools[1].(map[string]any)
	if !ok {
		t.Fatalf("compiled JSON tool[1] = %#v, want object", tools[1])
	}
	creds, ok := toolJSON["credentials"].([]any)
	if !ok {
		t.Fatalf("compiled JSON tool[1] credentials = %#v, want explicit empty array", toolJSON["credentials"])
	}
	if len(creds) != 0 {
		t.Fatalf("compiled JSON tool[1] credentials len = %d, want 0", len(creds))
	}

	loaded, err := ParsePkg(raw)
	if err != nil {
		t.Fatalf("ParsePkg() error: %v", err)
	}
	if loaded.Tools[0].CredentialsPresent {
		t.Fatal("ParsePkg() should keep absent tool credentials absent")
	}
	if !loaded.Tools[1].CredentialsPresent {
		t.Fatal("ParsePkg() should preserve explicit empty tool credentials")
	}
	if len(loaded.Tools[1].Credentials) != 0 {
		t.Fatalf("loaded tool[1] credentials len = %d, want 0", len(loaded.Tools[1].Credentials))
	}
	if !loaded.Tools[2].CredentialsPresent {
		t.Fatal("ParsePkg() should preserve explicit replacement tool credentials")
	}
	if diff := cmp.Diff(pkg.Tools[2].Credentials, loaded.Tools[2].Credentials); diff != "" {
		t.Fatalf("ParsePkg() tool replacement credentials mismatch (-want +got):\n%s", diff)
	}
}

func TestManifestToolCredentialValidation(t *testing.T) {
	t.Parallel()

	t.Run("dev manifest rejects malformed tool credential metadata", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "credentials": [
        {
          "name": "",
          "type": "nope",
          "inject": { "hosts": [""] }
        }
      ]
    }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "credentials") {
			t.Fatalf("ParseDev() error = %v, want credentials field mention", err)
		}
	})

	t.Run("dev manifest rejects tool credentials without module", func(t *testing.T) {
		t.Parallel()
		_, err := ParseDev([]byte(`{
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "credentials": [
        {
          "name": "google_workspace",
          "type": "bearer",
          "inject": { "hosts": ["www.googleapis.com"] }
        }
      ]
    }
  ]
}`))
		if err == nil {
			t.Fatal("ParseDev() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "module is required") || !strings.Contains(err.Error(), "tools[0].credentials") {
			t.Fatalf("ParseDev() error = %v, want module and tool credentials field mention", err)
		}
	})

	t.Run("schema rejects wrong tool credentials type in compiled manifest", func(t *testing.T) {
		t.Parallel()
		_, err := ParsePkg([]byte(`{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "effect": "readOnly",
      "credentials": "inherit"
    }
  ]
}`))
		if err == nil {
			t.Fatal("ParsePkg() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "credentials") {
			t.Fatalf("ParsePkg() error = %v, want credentials field mention", err)
		}
	})

	t.Run("validate compiled rejects invalid tool credential replacement", func(t *testing.T) {
		t.Parallel()
		_, err := ValidateCompiled(tooldef.Package{
			Module:  tooldef.ModulePath("github.com/example/google-workspace"),
			Name:    "google-workspace",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Tools: []tooldef.PackageTool{{
				EntryTS:            "tools/users.list.ts",
				Effect:             tooldef.EffectReadOnly,
				CredentialsPresent: true,
				Credentials: []tooldef.PackageCredential{{
					Name: "google_workspace",
					Type: tooldef.CredentialTypeBearer,
					Inject: tooldef.CredentialInject{
						Hosts: []string{""},
					},
				}},
			}},
		}, ValidationModeDist)
		if err == nil {
			t.Fatal("ValidateCompiled() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "tools[0].credentials[0].inject.hosts[0]") {
			t.Fatalf("ValidateCompiled() error = %v, want nested tool credential field mention", err)
		}
	})
}

func explicitProviderRef(authURL, tokenURL string) tooldef.OAuth2ProviderRef {
	return tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
		AuthURL:  authURL,
		TokenURL: tokenURL,
	}}
}

func boolPtr(v bool) *bool {
	return &v
}

func effectPtr(v tooldef.Effect) *tooldef.Effect {
	return &v
}
