package tool

import (
	"io/fs"

	"github.com/microsoft/typescript-go/toolbox"
)

// Package is the smallest useful static package shape for the first package-loading seam.
type Package struct {
	Module                    ModulePath          `json:"module,omitempty"`
	Name                      string              `json:"name"`
	Runtime                   ToolRuntime         `json:"runtime"`
	SHA256                    string              `json:"sha256,omitempty"`
	AdditionalTypeScriptGlobs []string            `json:"additionalTypeScriptGlobs,omitempty"`
	Executables               map[string]string   `json:"executables,omitempty"`
	AllowedHosts              []string            `json:"allowed_hosts,omitempty"`
	Credentials               []PackageCredential `json:"credentials,omitempty"`
	Tools                     []PackageTool       `json:"tools"`
}

type ToolRuntime string

const RuntimeTypeScriptSandbox ToolRuntime = "typescript-sandbox"

const RuntimeTypeScriptWasixSandbox ToolRuntime = "typescript+wasix-sandbox"

const RuntimeTypeScriptWasip2Sandbox ToolRuntime = "typescript+wasip2-sandbox"

type Effect string

const (
	EffectReadOnly     Effect = "readOnly"
	EffectReversible   Effect = "reversible"
	EffectIrreversible Effect = "irreversible"
)

type CredentialType string

const (
	CredentialTypeOAuth2 CredentialType = "oauth2"
	CredentialTypeAPIKey CredentialType = "api_key"
	CredentialTypeBearer CredentialType = "bearer"
	CredentialTypeCustom CredentialType = "custom"
)

type CredentialInject struct {
	Hosts      []string `json:"hosts,omitempty"`
	PathPrefix string   `json:"pathPrefix,omitempty"`
	Method     string   `json:"method,omitempty"`
}

type PackageCredential struct {
	Name      string           `json:"name"`
	Type      CredentialType   `json:"type"`
	Provider  string           `json:"provider,omitempty"`
	Scopes    []string         `json:"scopes,omitempty"`
	Inject    CredentialInject `json:"inject"`
	Strategy  string           `json:"strategy,omitempty"`
	AuthHosts []string         `json:"authHosts,omitempty"`
}

// ResourceParam describes one inferred resource parameter and its canonical binding name.
type ResourceParam struct {
	Name        string `json:"name"`         // e.g. "account_id"
	BindingName string `json:"binding_name"` // e.g. "zendesk_account" (defaults to Name)
}

type PackageTool struct {
	EntryTS            string                 `json:"entry_ts"`
	Idempotent         *bool                  `json:"idempotent,omitempty"`
	Effect             Effect                 `json:"effect,omitempty"`
	Description        string                 `json:"description,omitempty"`
	ParamsSchema       map[string]any         `json:"paramsSchema,omitempty"`
	Sig                *toolbox.FuncSignature `json:"-"`
	ResourceParams     []ResourceParam        `json:"resourceParams,omitempty"`
	AllowedHosts       []string               `json:"allowed_hosts,omitempty"`
	AllowedHostsExtend []string               `json:"allowed_hosts_extend,omitempty"`
}

// ResolvedTool is the smallest useful selected tool shape for the current
// outside-in seams. It combines static package identity with the concrete
// executable artifact for one visible tool.
type ResolvedTool struct {
	Name           string
	Description    string
	Sig            *toolbox.FuncSignature
	Effect         Effect
	Idempotent     *bool
	ResourceParams []ResourceParam
	AllowedHosts   []string
	Package        *Package
	TS             *TSToolDef
	TSWasm         *TSWasmToolDef

	// paramsSchema is the fallback JSON Schema for when Sig is nil (e.g. dist packages).
	// Use ParamsSchema() to access — it derives from Sig when available.
	paramsSchema map[string]any
}

// SetParamsSchema sets the fallback JSON Schema (used when Sig is nil).
func (rt *ResolvedTool) SetParamsSchema(schema map[string]any) {
	rt.paramsSchema = schema
}

// ParamsSchema returns the JSON Schema for this tool's parameters.
// When Sig is available, it derives the schema from the type signature
// using ParamsAsObject (which handles both single-param-object and
// multi-param functions); otherwise it falls back to the stored schema
// (e.g. from dist manifests).
func (rt ResolvedTool) ParamsSchema() map[string]any {
	if rt.Sig != nil {
		if pt := rt.Sig.ParamsAsObject(); pt != nil {
			return pt.ToJSONSchema()
		}
	}
	return rt.paramsSchema
}

// TSToolDef is the smallest useful TS tool definition for the current invoke
// seam. It points at one tool entry file inside a package-shaped filesystem.
type TSToolDef struct {
	Entry       string
	Files       fs.FS
	PackageRoot string
}

type TSWasmToolDef struct {
	TSToolDef
	Executables map[string]string
}
