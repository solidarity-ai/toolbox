package tool

import (
	"io/fs"

	"github.com/microsoft/typescript-go/toolbox"
)

// Package is the smallest useful static package shape for the first package-loading seam.
type Package struct {
	Module ModulePath `json:"module"`
	Name   string     `json:"name"`
	// UseWhenHint is optional short guidance for packages that are not obvious
	// from general model knowledge. Leave it empty for well-known services.
	UseWhenHint               string              `json:"useWhenHint,omitempty"`
	Runtime                   ToolRuntime         `json:"runtime"`
	SHA256                    string              `json:"sha256,omitempty"`
	AdditionalTypeScriptGlobs []string            `json:"additionalTypeScriptGlobs,omitempty"`
	Executables               map[string]string   `json:"executables,omitempty"`
	Tools                     []PackageTool       `json:"tools"`
	Credentials               []PackageCredential `json:"credentials,omitempty"`
	AllowedHosts              []string            `json:"allowed_hosts,omitempty"`
}

// PackageCredential declares a credential a package needs and how to inject it.
type PackageCredential struct {
	Name         string                `json:"name"`
	Type         string                `json:"type"`
	Instructions string                `json:"instructions,omitempty"`
	Provider     *OAuth2ProviderConfig `json:"provider,omitempty"`
	Scopes       []string              `json:"scopes,omitempty"`
	Inject       PackageInject         `json:"inject"`
}

// OAuth2ProviderConfig holds OAuth2 provider endpoint configuration.
type OAuth2ProviderConfig struct {
	Name       string            `json:"name,omitempty"`
	AuthURL    string            `json:"auth_url,omitempty"`
	TokenURL   string            `json:"token_url,omitempty"`
	AuthParams map[string]string `json:"auth_params,omitempty"`
	PKCE       *bool             `json:"pkce,omitempty"`
}

func (c *OAuth2ProviderConfig) PKCEEnabled() bool {
	if c == nil || c.PKCE == nil {
		return true
	}
	return *c.PKCE
}

// PackageInject describes how and where to inject a credential.
type PackageInject struct {
	Hosts                    []string `json:"hosts"`
	Method                   string   `json:"method"`
	HeaderName               string   `json:"header_name,omitempty"`
	PathPrefix               string   `json:"path_prefix,omitempty"`
	AllowUnsafeHTTPInjection bool     `json:"allow_unsafe_http_injection,omitempty"`
}

type ToolRuntime string

const RuntimeTypeScriptSandbox ToolRuntime = "typescript-sandbox"

const RuntimeTypeScriptWasixSandbox ToolRuntime = "typescript+wasix-sandbox"

const RuntimeTypeScriptWasip2Sandbox ToolRuntime = "typescript+wasip2-sandbox"

const RuntimeBuiltin ToolRuntime = "builtin"

type Effect string

const (
	EffectReadOnly     Effect = "readOnly"
	EffectReversible   Effect = "reversible"
	EffectIrreversible Effect = "irreversible"
)

// ResourceParam describes one inferred resource parameter and its canonical binding name.
type ResourceParam struct {
	Name        string `json:"name"`         // e.g. "account_id"
	BindingName string `json:"binding_name"` // e.g. "zendesk_account" (defaults to Name)
}

type PackageTool struct {
	EntryTS               string                 `json:"entry_ts"`
	Idempotent            *bool                  `json:"idempotent,omitempty"`
	Effect                Effect                 `json:"effect,omitempty"`
	Description           string                 `json:"description,omitempty"`
	MaxFetchResponseBytes *int64                 `json:"max_fetch_response_bytes,omitempty"`
	ParamsSchema          map[string]any         `json:"paramsSchema,omitempty"`
	Sig                   *toolbox.FuncSignature `json:"-"`
	ResourceParams        []ResourceParam        `json:"resourceParams,omitempty"`
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
