package tool

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"strings"

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
	HeaderName string   `json:"headerName,omitempty"`
	QueryName  string   `json:"queryName,omitempty"`
}

type OAuth2ProviderEndpoints struct {
	AuthURL  string `json:"auth_url"`
	TokenURL string `json:"token_url"`
}

type OAuth2ProviderRef struct {
	Name      string
	Endpoints *OAuth2ProviderEndpoints
}

func (r OAuth2ProviderRef) IsZero() bool {
	return strings.TrimSpace(r.Name) == "" && r.Endpoints == nil
}

func (r OAuth2ProviderRef) BuiltInName() string {
	return strings.TrimSpace(r.Name)
}

func (r OAuth2ProviderRef) ExplicitEndpoints() (OAuth2ProviderEndpoints, bool) {
	if r.Endpoints == nil {
		return OAuth2ProviderEndpoints{}, false
	}
	return *r.Endpoints, true
}

func (r OAuth2ProviderRef) MarshalJSON() ([]byte, error) {
	if endpoints, ok := r.ExplicitEndpoints(); ok {
		return json.Marshal(endpoints)
	}
	if name := r.BuiltInName(); name != "" {
		return json.Marshal(name)
	}
	return json.Marshal(nil)
}

func (r *OAuth2ProviderRef) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*r = OAuth2ProviderRef{}
		return nil
	}

	if len(trimmed) > 0 && trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(data, &name); err != nil {
			return err
		}
		*r = OAuth2ProviderRef{Name: name}
		return nil
	}

	var endpoints OAuth2ProviderEndpoints
	if err := json.Unmarshal(data, &endpoints); err != nil {
		return fmt.Errorf("oauth2 provider must be a built-in name or {auth_url, token_url} object: %w", err)
	}
	*r = OAuth2ProviderRef{Endpoints: &endpoints}
	return nil
}

type OAuth2ProviderConfig struct {
	Name     string `json:"name,omitempty"`
	AuthURL  string `json:"auth_url"`
	TokenURL string `json:"token_url"`
}

var knownOAuth2Providers = map[string]OAuth2ProviderConfig{
	"google": {
		Name:     "google",
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
	},
	"slack": {
		Name:     "slack",
		AuthURL:  "https://slack.com/oauth/v2/authorize",
		TokenURL: "https://slack.com/api/oauth.v2.access",
	},
	"microsoft": {
		Name:     "microsoft",
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	},
}

func KnownOAuth2Provider(name string) (OAuth2ProviderConfig, bool) {
	provider, ok := knownOAuth2Providers[strings.TrimSpace(name)]
	return provider, ok
}

func ResolveOAuth2Provider(ref OAuth2ProviderRef) (OAuth2ProviderConfig, error) {
	if endpoints, ok := ref.ExplicitEndpoints(); ok {
		config := OAuth2ProviderConfig{AuthURL: strings.TrimSpace(endpoints.AuthURL), TokenURL: strings.TrimSpace(endpoints.TokenURL)}
		if err := validateOAuth2ProviderConfig(config); err != nil {
			return OAuth2ProviderConfig{}, err
		}
		return config, nil
	}

	name := ref.BuiltInName()
	if name == "" {
		return OAuth2ProviderConfig{}, fmt.Errorf("oauth2 provider is required")
	}
	provider, ok := KnownOAuth2Provider(name)
	if !ok {
		return OAuth2ProviderConfig{}, fmt.Errorf("unknown oauth2 provider %q", name)
	}
	if err := validateOAuth2ProviderConfig(provider); err != nil {
		return OAuth2ProviderConfig{}, fmt.Errorf("oauth2 provider %q is invalid: %w", name, err)
	}
	return provider, nil
}

func validateOAuth2ProviderConfig(config OAuth2ProviderConfig) error {
	authURL := strings.TrimSpace(config.AuthURL)
	if authURL == "" {
		return fmt.Errorf("auth_url is required")
	}
	if err := validateAbsoluteHTTPSURL("auth_url", authURL); err != nil {
		return err
	}

	tokenURL := strings.TrimSpace(config.TokenURL)
	if tokenURL == "" {
		return fmt.Errorf("token_url is required")
	}
	if err := validateAbsoluteHTTPSURL("token_url", tokenURL); err != nil {
		return err
	}

	return nil
}

func validateAbsoluteHTTPSURL(field, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %q is invalid: %w", field, raw, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s %q must be an absolute url", field, raw)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%s %q must use https", field, raw)
	}
	return nil
}

type PackageCredential struct {
	Name      string            `json:"name"`
	Type      CredentialType    `json:"type"`
	Provider  OAuth2ProviderRef `json:"provider,omitempty"`
	Scopes    []string          `json:"scopes,omitempty"`
	Inject    CredentialInject  `json:"inject"`
	Strategy  string            `json:"strategy,omitempty"`
	AuthHosts []string          `json:"authHosts,omitempty"`
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
	Credentials        []PackageCredential    `json:"-"`
	CredentialsPresent bool                   `json:"-"`
}

type packageToolJSON struct {
	EntryTS            string               `json:"entry_ts"`
	Idempotent         *bool                `json:"idempotent,omitempty"`
	Effect             Effect               `json:"effect,omitempty"`
	Description        string               `json:"description,omitempty"`
	ParamsSchema       map[string]any       `json:"paramsSchema,omitempty"`
	ResourceParams     []ResourceParam      `json:"resourceParams,omitempty"`
	AllowedHosts       []string             `json:"allowed_hosts,omitempty"`
	AllowedHostsExtend []string             `json:"allowed_hosts_extend,omitempty"`
	Credentials        *[]PackageCredential `json:"credentials,omitempty"`
}

func (t PackageTool) MarshalJSON() ([]byte, error) {
	payload := packageToolJSON{
		EntryTS:            t.EntryTS,
		Idempotent:         t.Idempotent,
		Effect:             t.Effect,
		Description:        t.Description,
		ParamsSchema:       t.ParamsSchema,
		ResourceParams:     t.ResourceParams,
		AllowedHosts:       t.AllowedHosts,
		AllowedHostsExtend: t.AllowedHostsExtend,
	}
	if t.CredentialsPresent {
		credentials := make([]PackageCredential, len(t.Credentials))
		copy(credentials, t.Credentials)
		payload.Credentials = &credentials
	}
	return json.Marshal(payload)
}

func (t *PackageTool) UnmarshalJSON(data []byte) error {
	var payload packageToolJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var credentials []PackageCredential
	if payload.Credentials != nil {
		credentials = append([]PackageCredential(nil), (*payload.Credentials)...)
	}
	*t = PackageTool{
		EntryTS:            payload.EntryTS,
		Idempotent:         payload.Idempotent,
		Effect:             payload.Effect,
		Description:        payload.Description,
		ParamsSchema:       payload.ParamsSchema,
		ResourceParams:     payload.ResourceParams,
		AllowedHosts:       payload.AllowedHosts,
		AllowedHostsExtend: payload.AllowedHostsExtend,
		Credentials:        credentials,
	}
	_, t.CredentialsPresent = raw["credentials"]
	return nil
}

// ResolvedTool is the smallest useful selected tool shape for the current
// outside-in seams. It combines static package identity with the concrete
// executable artifact for one visible tool.
type ResolvedTool struct {
	Name                 string
	Description          string
	Sig                  *toolbox.FuncSignature
	Effect               Effect
	Idempotent           *bool
	ResourceParams       []ResourceParam
	AllowedHosts         []string
	EffectiveCredentials []PackageCredential
	Package              *Package
	TS                   *TSToolDef
	TSWasm               *TSWasmToolDef

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
