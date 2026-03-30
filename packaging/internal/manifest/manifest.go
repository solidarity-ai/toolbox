package manifest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jinzhu/inflection"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

//go:embed toolbox_pkg.dev.schema.json
var toolboxPkgDevSchemaJSON []byte

//go:embed toolbox_pkg.dist.schema.json
var toolboxPkgDistSchemaJSON []byte

var resolvedToolboxPkgDevSchema = mustResolveSchema(toolboxPkgDevSchemaJSON)
var resolvedToolboxPkgDistSchema = mustResolveSchema(toolboxPkgDistSchemaJSON)

const (
	DevManifestFilename = "toolbox.devpkg.json"
	PkgManifestFilename = "toolbox.pkg.json"
)

type ValidationMode int

const (
	ValidationModeDev ValidationMode = iota
	ValidationModeDist
)

type Warning struct {
	Message string
}

type LoadResult struct {
	Package  tooldef.Package
	Warnings []Warning
}

// DevManifest is the source authoring format read from toolbox.devpkg.json.
type DevManifest struct {
	Module                    tooldef.ModulePath          `json:"module,omitempty"`
	Name                      string                      `json:"name"`
	Runtime                   tooldef.ToolRuntime         `json:"runtime"`
	AdditionalTypeScriptGlobs []string                    `json:"additionalTypeScriptGlobs"`
	Executables               map[string]string           `json:"executables"`
	AllowedHosts              []string                    `json:"allowed_hosts,omitempty"`
	Credentials               []tooldef.PackageCredential `json:"credentials,omitempty"`
	Tools                     []DevManifestTool           `json:"tools"`
}

// DevManifestToolResource groups resource-related overrides for a tool.
type DevManifestToolResource struct {
	Bindings map[string]string `json:"bindings,omitempty"`
	Mode     string            `json:"mode,omitempty"` // "collection" or "member", empty means infer
}

type DevManifestTool struct {
	EntryTS            string                      `json:"entry_ts"`
	Idempotent         *bool                       `json:"idempotent"`
	Effect             *tooldef.Effect             `json:"effect"`
	Resource           *DevManifestToolResource    `json:"resource,omitempty"`
	AllowedHosts       []string                    `json:"allowed_hosts,omitempty"`
	AllowedHostsExtend []string                    `json:"allowed_hosts_extend,omitempty"`
	Credentials        []tooldef.PackageCredential `json:"-"`
	CredentialsPresent bool                        `json:"-"`
}

type devManifestToolJSON struct {
	EntryTS            string                       `json:"entry_ts"`
	Idempotent         *bool                        `json:"idempotent"`
	Effect             *tooldef.Effect              `json:"effect"`
	Resource           *DevManifestToolResource     `json:"resource,omitempty"`
	AllowedHosts       []string                     `json:"allowed_hosts,omitempty"`
	AllowedHostsExtend []string                     `json:"allowed_hosts_extend,omitempty"`
	Credentials        *[]tooldef.PackageCredential `json:"credentials,omitempty"`
}

func (t DevManifestTool) MarshalJSON() ([]byte, error) {
	payload := devManifestToolJSON{
		EntryTS:            t.EntryTS,
		Idempotent:         t.Idempotent,
		Effect:             t.Effect,
		Resource:           t.Resource,
		AllowedHosts:       t.AllowedHosts,
		AllowedHostsExtend: t.AllowedHostsExtend,
	}
	if t.CredentialsPresent {
		credentials := make([]tooldef.PackageCredential, len(t.Credentials))
		copy(credentials, t.Credentials)
		payload.Credentials = &credentials
	}
	return json.Marshal(payload)
}

func (t *DevManifestTool) UnmarshalJSON(data []byte) error {
	var payload devManifestToolJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var credentials []tooldef.PackageCredential
	if payload.Credentials != nil {
		credentials = append([]tooldef.PackageCredential(nil), (*payload.Credentials)...)
	}
	*t = DevManifestTool{
		EntryTS:            payload.EntryTS,
		Idempotent:         payload.Idempotent,
		Effect:             payload.Effect,
		Resource:           payload.Resource,
		AllowedHosts:       payload.AllowedHosts,
		AllowedHostsExtend: payload.AllowedHostsExtend,
		Credentials:        credentials,
	}
	_, t.CredentialsPresent = raw["credentials"]
	return nil
}

func mustResolveSchema(raw []byte) *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		panic(fmt.Errorf("manifest: unmarshal embedded package schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("manifest: resolve embedded package schema: %w", err))
	}
	return resolved
}

// ParseDev parses and validates a source dev manifest (toolbox.devpkg.json).
func ParseDev(data []byte) (DevManifest, error) {
	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return DevManifest{}, fmt.Errorf("parse dev manifest: %w", err)
	}
	if err := resolvedToolboxPkgDevSchema.Validate(instance); err != nil {
		return DevManifest{}, fmt.Errorf("validate dev manifest: %w", err)
	}

	var manifest DevManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return DevManifest{}, fmt.Errorf("parse dev manifest: %w", err)
	}
	if err := validatePackageMetadata(manifest.Module, manifest.AllowedHosts, manifest.Credentials); err != nil {
		return DevManifest{}, fmt.Errorf("validate dev manifest metadata: %w", err)
	}
	for i, tool := range manifest.Tools {
		if err := validateEntryName(tool.EntryTS); err != nil {
			return DevManifest{}, fmt.Errorf("invalid tool entry %q: %w", tool.EntryTS, err)
		}
		if err := validateToolMetadata(fmt.Sprintf("tools[%d]", i), manifest.Module, tool); err != nil {
			return DevManifest{}, fmt.Errorf("validate dev manifest metadata: %w", err)
		}
	}
	return manifest, nil
}

// ParsePkg parses a compiled package manifest (toolbox.pkg.json).
func ParsePkg(data []byte) (tooldef.Package, error) {
	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	if err := resolvedToolboxPkgDistSchema.Validate(instance); err != nil {
		return tooldef.Package{}, fmt.Errorf("validate pkg manifest: %w", err)
	}

	var pkg tooldef.Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	if err := validatePackageMetadata(pkg.Module, pkg.AllowedHosts, pkg.Credentials); err != nil {
		return tooldef.Package{}, fmt.Errorf("validate pkg manifest metadata: %w", err)
	}
	for i, tool := range pkg.Tools {
		if err := validatePackageToolMetadata(fmt.Sprintf("tools[%d]", i), pkg.Module, tool); err != nil {
			return tooldef.Package{}, fmt.Errorf("validate pkg manifest metadata: %w", err)
		}
	}
	return pkg, nil
}

// Compile converts a dev manifest into a compiled package form,
// applying inference rules for missing fields.
func Compile(dev DevManifest) tooldef.Package {
	pkg := tooldef.Package{
		Module:                    dev.Module,
		Name:                      dev.Name,
		Runtime:                   dev.Runtime,
		AdditionalTypeScriptGlobs: append([]string(nil), dev.AdditionalTypeScriptGlobs...),
		Executables:               dev.Executables,
		AllowedHosts:              append([]string(nil), dev.AllowedHosts...),
		Credentials:               append([]tooldef.PackageCredential(nil), dev.Credentials...),
		Tools:                     make([]tooldef.PackageTool, len(dev.Tools)),
	}
	for i, tool := range dev.Tools {
		effect := InferEffect(tool.EntryTS)
		if tool.Effect != nil {
			effect = *tool.Effect
		}

		var resourceMode string
		var resourceBindings map[string]string
		if tool.Resource != nil {
			resourceMode = tool.Resource.Mode
			resourceBindings = tool.Resource.Bindings
		}
		resourceParams := InferResourceParamsWithMode(tool.EntryTS, resourceMode)
		// Apply manifest overrides for binding names
		for j := range resourceParams {
			if override, ok := resourceBindings[resourceParams[j].Name]; ok {
				resourceParams[j].BindingName = override
			}
		}

		pkg.Tools[i] = tooldef.PackageTool{
			EntryTS:            tool.EntryTS,
			Idempotent:         tool.Idempotent,
			Effect:             effect,
			ResourceParams:     resourceParams,
			AllowedHosts:       append([]string(nil), tool.AllowedHosts...),
			AllowedHostsExtend: append([]string(nil), tool.AllowedHostsExtend...),
			Credentials:        append([]tooldef.PackageCredential(nil), tool.Credentials...),
			CredentialsPresent: tool.CredentialsPresent,
		}
	}
	return pkg
}

// ValidateCompiled validates a compiled package against both the dev (lenient)
// and dist (strict) schemas. In dev mode, dist violations are returned as
// warnings. In dist mode, they are errors.
func ValidateCompiled(pkg tooldef.Package, mode ValidationMode) ([]Warning, error) {
	if err := validatePackageMetadata(pkg.Module, pkg.AllowedHosts, pkg.Credentials); err != nil {
		return nil, fmt.Errorf("validate compiled package metadata: %w", err)
	}
	for i, tool := range pkg.Tools {
		if err := validatePackageToolMetadata(fmt.Sprintf("tools[%d]", i), pkg.Module, tool); err != nil {
			return nil, fmt.Errorf("validate compiled package metadata: %w", err)
		}
	}

	raw, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("marshal compiled package: %w", err)
	}

	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return nil, fmt.Errorf("unmarshal compiled package: %w", err)
	}
	if err := resolvedToolboxPkgDevSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}

	var warnings []Warning
	if err := resolvedToolboxPkgDistSchema.Validate(instance); err != nil {
		if mode == ValidationModeDist {
			return nil, fmt.Errorf("validate compiled package for distribution: %w", err)
		}
		warnings = append(warnings, Warning{Message: fmt.Sprintf("distribution validation: %v", err)})
	}
	return warnings, nil
}

func validatePackageMetadata(module tooldef.ModulePath, allowedHosts []string, credentials []tooldef.PackageCredential) error {
	if module != "" {
		if _, err := tooldef.ParseModulePath(module.String()); err != nil {
			return fmt.Errorf("invalid module %q: %w", module, err)
		}
	}
	if err := validateHostList("allowed_hosts", allowedHosts); err != nil {
		return err
	}
	if err := validateCredentialList("credentials", module, credentials); err != nil {
		return err
	}
	return nil
}

func validateToolMetadata(prefix string, module tooldef.ModulePath, tool DevManifestTool) error {
	if err := validateToolAllowedHosts(prefix, tool.AllowedHosts, tool.AllowedHostsExtend); err != nil {
		return err
	}
	if err := validateCredentialList(prefix+".credentials", module, tool.Credentials); err != nil {
		return err
	}
	return nil
}

func validatePackageToolMetadata(prefix string, module tooldef.ModulePath, tool tooldef.PackageTool) error {
	if err := validateToolAllowedHosts(prefix, tool.AllowedHosts, tool.AllowedHostsExtend); err != nil {
		return err
	}
	if err := validateCredentialList(prefix+".credentials", module, tool.Credentials); err != nil {
		return err
	}
	return nil
}

func validateCredentialList(field string, module tooldef.ModulePath, credentials []tooldef.PackageCredential) error {
	if len(credentials) == 0 {
		return nil
	}
	if module == "" {
		return fmt.Errorf("module is required when %s are declared", field)
	}

	seenNames := make(map[string]struct{}, len(credentials))
	for i, cred := range credentials {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		name := strings.TrimSpace(cred.Name)
		if name == "" {
			return fmt.Errorf("%s.name must not be empty", prefix)
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("%s.name %q is duplicated", prefix, name)
		}
		seenNames[name] = struct{}{}

		switch cred.Type {
		case tooldef.CredentialTypeOAuth2, tooldef.CredentialTypeAPIKey, tooldef.CredentialTypeBearer, tooldef.CredentialTypeCustom:
			// ok
		default:
			return fmt.Errorf("%s.type %q is invalid", prefix, cred.Type)
		}

		method := strings.TrimSpace(cred.Inject.Method)
		if method == "" {
			method = "bearer_header"
		}
		if !supportedInjectMethod(method) {
			return fmt.Errorf("%s.inject.method %q is invalid", prefix, cred.Inject.Method)
		}
		if err := validateCredentialInjectConfig(prefix+".inject", method, cred.Inject); err != nil {
			return err
		}

		if err := validateCredentialProvider(prefix, cred); err != nil {
			return err
		}

		if len(cred.Inject.Hosts) == 0 {
			return fmt.Errorf("%s.inject.hosts must declare at least one host", prefix)
		}
		for j, host := range cred.Inject.Hosts {
			if strings.TrimSpace(host) == "" {
				return fmt.Errorf("%s.inject.hosts[%d] must not be empty", prefix, j)
			}
		}
	}
	return nil
}

func validateCredentialProvider(prefix string, cred tooldef.PackageCredential) error {
	switch cred.Type {
	case tooldef.CredentialTypeOAuth2:
		if _, err := tooldef.ResolveOAuth2Provider(cred.Provider); err != nil {
			return fmt.Errorf("%s.provider: %w", prefix, err)
		}
	case tooldef.CredentialTypeAPIKey, tooldef.CredentialTypeBearer, tooldef.CredentialTypeCustom:
		if _, ok := cred.Provider.ExplicitEndpoints(); ok {
			return fmt.Errorf("%s.provider explicit oauth2 endpoints are only allowed when type is oauth2", prefix)
		}
	}
	return nil
}

func validateToolAllowedHosts(prefix string, allowedHosts []string, allowedHostsExtend []string) error {
	if err := validateHostList(prefix+".allowed_hosts", allowedHosts); err != nil {
		return err
	}
	if err := validateHostList(prefix+".allowed_hosts_extend", allowedHostsExtend); err != nil {
		return err
	}
	if len(allowedHosts) > 0 && len(allowedHostsExtend) > 0 {
		return fmt.Errorf("%s must not declare both allowed_hosts and allowed_hosts_extend", prefix)
	}
	return nil
}

func validateHostList(field string, hosts []string) error {
	for i, host := range hosts {
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("%s[%d] must not be empty", field, i)
		}
	}
	return nil
}

func supportedInjectMethod(method string) bool {
	switch method {
	case "bearer_header", "basic_auth", "api_key_header", "api_key_query":
		return true
	default:
		return false
	}
}

func validateCredentialInjectConfig(field string, method string, inject tooldef.CredentialInject) error {
	headerName := strings.TrimSpace(inject.HeaderName)
	queryName := strings.TrimSpace(inject.QueryName)

	switch method {
	case "bearer_header", "basic_auth":
		if headerName != "" {
			return fmt.Errorf("%s.headerName is only allowed for api_key_header", field)
		}
		if queryName != "" {
			return fmt.Errorf("%s.queryName is only allowed for api_key_query", field)
		}
	case "api_key_header":
		if headerName == "" {
			return fmt.Errorf("%s.headerName must not be empty when method is api_key_header", field)
		}
		if !validHTTPHeaderName(headerName) {
			return fmt.Errorf("%s.headerName %q is invalid", field, inject.HeaderName)
		}
		if queryName != "" {
			return fmt.Errorf("%s.queryName is only allowed for api_key_query", field)
		}
	case "api_key_query":
		if queryName == "" {
			return fmt.Errorf("%s.queryName must not be empty when method is api_key_query", field)
		}
		if !validQueryParamName(queryName) {
			return fmt.Errorf("%s.queryName %q is invalid", field, inject.QueryName)
		}
		if headerName != "" {
			return fmt.Errorf("%s.headerName is only allowed for api_key_header", field)
		}
	}

	return nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c > 127 {
			return false
		}
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' ||
			c == '\'' || c == '*' || c == '+' || c == '-' || c == '.' ||
			c == '^' || c == '_' || c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

func validQueryParamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch r {
		case '&', '=', '#', '?':
			return false
		}
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// InferEffect derives an effect from the tool entry filename verb.
func InferEffect(entryTS string) tooldef.Effect {
	verb := inferVerb(entryTS)
	switch verb {
	case "list", "get", "read", "fetch", "search", "find", "describe":
		return tooldef.EffectReadOnly
	case "create", "add", "clone", "new":
		return tooldef.EffectReversible
	case "update", "delete", "remove", "set", "put", "patch", "replace", "edit", "send", "post":
		return tooldef.EffectIrreversible
	default:
		return tooldef.EffectIrreversible
	}
}

// InferToolName derives the tool name from the entry filename.
func InferToolName(entryTS string) string {
	base := filepath.Base(entryTS)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	// Convert kebab-case segments to camelCase: "async-complex" → "asyncComplex"
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = kebabToCamel(part)
	}
	return strings.Join(parts, ".")
}

// kebabToCamel converts a kebab-case string to camelCase.
func kebabToCamel(s string) string {
	segments := strings.Split(s, "-")
	for i := 1; i < len(segments); i++ {
		if len(segments[i]) > 0 {
			segments[i] = strings.ToUpper(segments[i][:1]) + segments[i][1:]
		}
	}
	return strings.Join(segments, "")
}

// kebabSegmentRe matches a valid kebab-case segment: lowercase letters, digits, and hyphens.
// Must start with a letter, must not start or end with a hyphen, no consecutive hyphens.
var kebabSegmentRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// validateEntryName checks that a tool entry path uses kebab-case naming.
// Expected format: "tools/<package>.<method>.ts" where each dot-separated
// segment of the name is kebab-case (e.g. "tools/edge-cases.async-complex.ts").
func validateEntryName(entryTS string) error {
	base := filepath.Base(entryTS)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	segments := strings.Split(name, ".")
	for _, seg := range segments {
		if !kebabSegmentRe.MatchString(seg) {
			return fmt.Errorf("segment %q is not valid kebab-case (expected lowercase letters, digits, and hyphens)", seg)
		}
	}
	return nil
}

// ResourceParam describes one inferred resource parameter.
type ResourceParam = tooldef.ResourceParam

// InferResourceParams derives resource parameters from the tool entry filename.
//
// Convention: "users.calendars.events.list.ts" -> user_id, calendar_id.
// The verb (last segment) is stripped. For "list" the deepest resource ID is
// excluded; for "get"/"update"/"delete" it is included.
// Only applies when there are 3+ segments (resource.subresource.verb).
func InferResourceParams(entryTS string) []ResourceParam {
	return InferResourceParamsWithMode(entryTS, "")
}

// InferResourceParamsWithMode is like InferResourceParams but accepts an
// optional mode override. When mode is "collection", the deepest resource ID
// is always excluded. When mode is "member", it is always included. When mode
// is empty, the current isCollectionMethod inference is used.
func InferResourceParamsWithMode(entryTS string, mode string) []ResourceParam {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, ".")

	// Need at least 3 parts: resource.subresource.verb
	if len(parts) < 3 {
		return nil
	}

	verb := parts[len(parts)-1]
	resources := parts[:len(parts)-1] // all segments except verb

	// Determine whether to treat as collection (exclude deepest ID) or member
	// (include deepest ID), based on mode override or verb inference.
	var collection bool
	switch mode {
	case "collection":
		collection = true
	case "member":
		collection = false
	default:
		collection = isCollectionMethod(verb)
	}

	count := len(resources)
	if collection {
		count = len(resources) - 1
	}

	if count <= 0 {
		return nil
	}

	params := make([]ResourceParam, 0, count)
	for i := 0; i < count; i++ {
		name := singularize(resources[i]) + "_id"
		params = append(params, ResourceParam{
			Name:        name,
			BindingName: name,
		})
	}
	return params
}

// isCollectionMethod returns true for verbs that operate on a collection
// (and therefore don't need the deepest resource ID).
func isCollectionMethod(verb string) bool {
	switch verb {
	case "list", "create", "add", "append", "search", "find", "new", "send", "post":
		return true
	default:
		return false
	}
}

// singularize converts a plural resource name to its singular form
// using jinzhu/inflection for Rails-style irregular handling.
func singularize(s string) string {
	return inflection.Singular(s)
}

func inferVerb(entryTS string) string {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
