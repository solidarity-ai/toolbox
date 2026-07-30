package manifest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
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

// DevManifest is the source authoring format read from toolbox.devpkg.json.
type DevManifest struct {
	MinimumToolboxVersion     tooldef.Version         `json:"minimumToolboxVersion,omitempty"`
	Module                    tooldef.ModulePath      `json:"module"`
	Name                      string                  `json:"name"`
	UseWhenHint               string                  `json:"useWhenHint,omitempty"`
	Runtime                   tooldef.ToolRuntime     `json:"runtime"`
	AdditionalTypeScriptGlobs []string                `json:"additionalTypeScriptGlobs"`
	Executables               map[string]string       `json:"executables"`
	Resources                 []DevManifestResource   `json:"resources,omitempty"`
	Tools                     []DevManifestTool       `json:"tools"`
	Credentials               []DevManifestCredential `json:"credentials,omitempty"`
	AllowedHosts              []string                `json:"allowed_hosts,omitempty"`
}

// DevManifestCredential declares a credential requirement in the dev manifest.
type DevManifestCredential struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Instructions string            `json:"instructions,omitempty"`
	Provider     json.RawMessage   `json:"provider,omitempty"`
	Scopes       []string          `json:"scopes,omitempty"`
	Inject       DevManifestInject `json:"inject"`
}

// DevManifestInject describes injection targets in the dev manifest.
type DevManifestInject struct {
	Hosts                    []string `json:"hosts"`
	Method                   string   `json:"method"`
	HeaderName               string   `json:"header_name,omitempty"`
	PathPrefix               string   `json:"path_prefix,omitempty"`
	AllowUnsafeHTTPInjection bool     `json:"allow_unsafe_http_injection,omitempty"`
}

// DevManifestResource declares one package-level resource selector.
type DevManifestResource struct {
	Path   string                     `json:"path"`
	Params []DevManifestResourceParam `json:"params"`
}

type DevManifestResourceParam struct {
	Name        string `json:"name"`
	BindingName string `json:"binding_name,omitempty"`
}

type DevManifestTool struct {
	EntryTS    string          `json:"entry_ts"`
	Idempotent *bool           `json:"idempotent"`
	Effect     *tooldef.Effect `json:"effect"`
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
	if err := validateModulePath(manifest.Module); err != nil {
		return DevManifest{}, fmt.Errorf("parse dev manifest: %w", err)
	}
	if err := validateUseWhenHint(manifest.UseWhenHint); err != nil {
		return DevManifest{}, fmt.Errorf("parse dev manifest: %w", err)
	}
	for _, tool := range manifest.Tools {
		if err := validateEntryName(tool.EntryTS); err != nil {
			return DevManifest{}, fmt.Errorf("invalid tool entry %q: %w", tool.EntryTS, err)
		}
	}
	if err := validateDevResources(manifest.Resources); err != nil {
		return DevManifest{}, fmt.Errorf("parse dev manifest: %w", err)
	}
	return manifest, nil
}

// ParsePkg parses a compiled package manifest (toolbox.pkg.json).
func ParsePkg(data []byte) (tooldef.Package, error) {
	var pkg tooldef.Package
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pkg); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: trailing JSON")
	}
	if err := validateModulePath(pkg.Module); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	if err := validateUseWhenHint(pkg.UseWhenHint); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	if err := validateCompiledResources(pkg.Resources); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	return pkg, nil
}

// Compile converts a dev manifest into a compiled package form,
// applying inference rules for missing fields.
func Compile(dev DevManifest) tooldef.Package {
	pkg := tooldef.Package{
		MinimumToolboxVersion:     dev.MinimumToolboxVersion,
		Module:                    dev.Module,
		Name:                      dev.Name,
		UseWhenHint:               strings.TrimSpace(dev.UseWhenHint),
		Runtime:                   dev.Runtime,
		AdditionalTypeScriptGlobs: append([]string(nil), dev.AdditionalTypeScriptGlobs...),
		Executables:               dev.Executables,
		Resources:                 compileResources(dev.Resources),
		Tools:                     make([]tooldef.PackageTool, len(dev.Tools)),
		AllowedHosts:              append([]string(nil), dev.AllowedHosts...),
	}
	if len(pkg.AllowedHosts) == 0 {
		pkg.AllowedHosts = nil
	}
	for i, tool := range dev.Tools {
		effect := InferEffect(tool.EntryTS)
		if tool.Effect != nil {
			effect = *tool.Effect
		}

		pkg.Tools[i] = tooldef.PackageTool{
			EntryTS:    tool.EntryTS,
			Idempotent: tool.Idempotent,
			Effect:     effect,
		}
	}
	for _, cred := range dev.Credentials {
		pc := tooldef.PackageCredential{
			Name:         cred.Name,
			Type:         cred.Type,
			Instructions: cred.Instructions,
			Scopes:       cred.Scopes,
			Inject: tooldef.PackageInject{
				Hosts:                    cred.Inject.Hosts,
				Method:                   cred.Inject.Method,
				HeaderName:               cred.Inject.HeaderName,
				PathPrefix:               cred.Inject.PathPrefix,
				AllowUnsafeHTTPInjection: cred.Inject.AllowUnsafeHTTPInjection,
			},
		}
		pc.Provider = compileProvider(cred.Provider)
		pkg.Credentials = append(pkg.Credentials, pc)
	}
	return pkg
}

// compileProvider resolves the provider field from a dev manifest credential.
// It may be a string (known provider name) or an object with auth_url/token_url.
func compileProvider(raw json.RawMessage) *tooldef.OAuth2ProviderConfig {
	if len(raw) == 0 {
		return nil
	}

	// Try string first (known provider name).
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return &tooldef.OAuth2ProviderConfig{Name: name}
	}

	// Try object form (named provider with optional auth_params, or custom URLs).
	var obj struct {
		Name       string            `json:"name"`
		AuthURL    string            `json:"auth_url"`
		TokenURL   string            `json:"token_url"`
		AuthParams map[string]string `json:"auth_params"`
		PKCE       *bool             `json:"pkce"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Name != "" {
			return &tooldef.OAuth2ProviderConfig{
				Name:       obj.Name,
				AuthParams: obj.AuthParams,
				PKCE:       obj.PKCE,
			}
		}
		if obj.AuthURL != "" || obj.TokenURL != "" {
			return &tooldef.OAuth2ProviderConfig{
				AuthURL:    obj.AuthURL,
				TokenURL:   obj.TokenURL,
				AuthParams: obj.AuthParams,
				PKCE:       obj.PKCE,
			}
		}
	}

	return nil
}

// ValidateCompiled validates a compiled package against both the dev (lenient)
// and dist (strict) schemas. In dev mode, dist violations are returned as
// warnings. In dist mode, they are errors.
func ValidateCompiled(pkg tooldef.Package, mode ValidationMode) ([]Warning, error) {
	if err := validateCompiledVersions(pkg); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}
	if err := validateModulePath(pkg.Module); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}
	if err := validateUseWhenHint(pkg.UseWhenHint); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}
	if err := validateCompiledResources(pkg.Resources); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}

	devPkg := pkg
	devPkg.ManifestSchemaVersion = 0
	devPkg.PackedByToolboxVersion = ""
	instance, err := compiledInstance(devPkg)
	if err != nil {
		return nil, err
	}
	if err := resolvedToolboxPkgDevSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("validate compiled package: %w", err)
	}

	distPkg := pkg
	if mode == ValidationModeDev {
		distPkg.ManifestSchemaVersion = tooldef.PackageManifestSchemaVersion
		if distPkg.MinimumToolboxVersion == "" {
			distPkg.MinimumToolboxVersion = "v0.0.0"
		}
		distPkg.PackedByToolboxVersion = "v0.0.0"
	}
	instance, err = compiledInstance(distPkg)
	if err != nil {
		return nil, err
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

func compiledInstance(pkg tooldef.Package) (map[string]any, error) {
	raw, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("marshal compiled package: %w", err)
	}
	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return nil, fmt.Errorf("unmarshal compiled package: %w", err)
	}
	return instance, nil
}

func validateCompiledVersions(pkg tooldef.Package) error {
	if pkg.ManifestSchemaVersion != 0 && pkg.ManifestSchemaVersion != tooldef.PackageManifestSchemaVersion {
		return fmt.Errorf("unsupported manifestSchemaVersion %d", pkg.ManifestSchemaVersion)
	}
	for name, version := range map[string]tooldef.Version{
		"minimumToolboxVersion":  pkg.MinimumToolboxVersion,
		"packedByToolboxVersion": pkg.PackedByToolboxVersion,
	} {
		if version == "" {
			continue
		}
		parsed, err := tooldef.ParseVersion(version.String())
		if err != nil || !parsed.IsRelease() {
			return fmt.Errorf("%s %q must be a released semantic version", name, version)
		}
	}
	return nil
}

func validateModulePath(module tooldef.ModulePath) error {
	if _, err := tooldef.ParseModulePath(module.String()); err != nil {
		return fmt.Errorf("invalid module %q: %w", module, err)
	}
	return nil
}

func validateUseWhenHint(useWhenHint string) error {
	useWhenHint = strings.TrimSpace(useWhenHint)
	if len(useWhenHint) > 100 {
		return fmt.Errorf("useWhenHint must be 100 characters or fewer")
	}
	return nil
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

func compileResources(resources []DevManifestResource) []tooldef.Resource {
	if len(resources) == 0 {
		return nil
	}
	out := make([]tooldef.Resource, 0, len(resources))
	for _, resource := range resources {
		compiled := tooldef.Resource{
			Path:   normalizeResourcePath(resource.Path),
			Params: make([]tooldef.ResourceParam, 0, len(resource.Params)),
		}
		for _, param := range resource.Params {
			bindingName := strings.TrimSpace(param.BindingName)
			if bindingName == "" {
				bindingName = param.Name
			}
			compiled.Params = append(compiled.Params, tooldef.ResourceParam{
				Name:        param.Name,
				BindingName: bindingName,
			})
		}
		out = append(out, compiled)
	}
	return out
}

func validateDevResources(resources []DevManifestResource) error {
	return validateResources(resources, kebabSegmentRe, normalizeResourcePath, false,
		func(resource DevManifestResource) (string, []DevManifestResourceParam) {
			return resource.Path, resource.Params
		},
		func(param DevManifestResourceParam) (string, string) { return param.Name, param.BindingName },
	)
}

var compiledResourceSegmentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func validateCompiledResources(resources []tooldef.Resource) error {
	return validateResources(resources, compiledResourceSegmentRe, strings.TrimSpace, true,
		func(resource tooldef.Resource) (string, []tooldef.ResourceParam) {
			return resource.Path, resource.Params
		},
		func(param tooldef.ResourceParam) (string, string) { return param.Name, param.BindingName },
	)
}

func validateResources[R, P any](
	resources []R,
	segmentPattern *regexp.Regexp,
	normalizePath func(string) string,
	requireBinding bool,
	fields func(R) (string, []P),
	paramFields func(P) (string, string),
) error {
	seenPaths := make(map[string]map[string]bool, len(resources))
	for _, resource := range resources {
		rawPath, params := fields(resource)
		path := strings.TrimSpace(rawPath)
		if path == "" {
			return fmt.Errorf("resource path must not be empty")
		}
		for _, part := range strings.Split(path, ".") {
			if !segmentPattern.MatchString(part) {
				return fmt.Errorf("resource path %q has invalid segment %q", rawPath, part)
			}
		}
		normalized := normalizePath(path)
		if _, ok := seenPaths[normalized]; ok {
			return fmt.Errorf("duplicate resource path %q", rawPath)
		}
		if len(params) == 0 {
			return fmt.Errorf("resource %q must declare at least one selector parameter", rawPath)
		}
		seenParams := map[string]bool{}
		for _, param := range params {
			name, binding := paramFields(param)
			name = strings.TrimSpace(name)
			if name == "" || requireBinding && strings.TrimSpace(binding) == "" {
				if requireBinding {
					return fmt.Errorf("resource %q contains an empty selector parameter name or binding name", rawPath)
				}
				return fmt.Errorf("resource %q contains an empty selector parameter name", rawPath)
			}
			if seenParams[name] {
				return fmt.Errorf("resource %q contains duplicate selector parameter %q", rawPath, name)
			}
			seenParams[name] = true
		}
		seenPaths[normalized] = seenParams
	}
	for path, params := range seenPaths {
		parts := strings.Split(path, ".")
		for depth := 1; depth < len(parts); depth++ {
			ancestor := strings.Join(parts[:depth], ".")
			ancestorParams, ok := seenPaths[ancestor]
			if !ok {
				continue
			}
			for name := range params {
				if ancestorParams[name] {
					return fmt.Errorf("resource %q repeats selector parameter %q from ancestor %q", path, name, ancestor)
				}
			}
		}
	}
	return nil
}

func normalizeResourcePath(path string) string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	for i, part := range parts {
		parts[i] = kebabToCamel(part)
	}
	return strings.Join(parts, ".")
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
