package manifest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
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

type LoadResult struct {
	Package  tooldef.Package
	Warnings []Warning
}

// DevManifest is the source authoring format read from toolbox.devpkg.json.
type DevManifest struct {
	Name                      string              `json:"name"`
	Runtime                   tooldef.ToolRuntime `json:"runtime"`
	AdditionalTypeScriptGlobs []string            `json:"additionalTypeScriptGlobs"`
	Executables               map[string]string   `json:"executables"`
	Tools                     []DevManifestTool   `json:"tools"`
}

type DevManifestTool struct {
	EntryTS          string              `json:"entry_ts"`
	Idempotent       *bool               `json:"idempotent"`
	AccessMode       *tooldef.AccessMode `json:"accessMode"`
	ResourceBindings map[string]string   `json:"resource_bindings,omitempty"` // param name -> canonical binding name
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
	return manifest, nil
}

// ParsePkg parses a compiled package manifest (toolbox.pkg.json).
func ParsePkg(data []byte) (tooldef.Package, error) {
	var pkg tooldef.Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return tooldef.Package{}, fmt.Errorf("parse pkg manifest: %w", err)
	}
	return pkg, nil
}

// Compile converts a dev manifest into a compiled package form,
// applying inference rules for missing fields.
func Compile(dev DevManifest) tooldef.Package {
	pkg := tooldef.Package{
		Name:                      dev.Name,
		Runtime:                   dev.Runtime,
		AdditionalTypeScriptGlobs: append([]string(nil), dev.AdditionalTypeScriptGlobs...),
		Executables:               dev.Executables,
		Tools:                     make([]tooldef.PackageTool, len(dev.Tools)),
	}
	for i, tool := range dev.Tools {
		accessMode := InferAccessMode(tool.EntryTS)
		if tool.AccessMode != nil {
			accessMode = *tool.AccessMode
		}

		resourceParams := InferResourceParams(tool.EntryTS)
		// Apply manifest overrides for binding names
		for j := range resourceParams {
			if override, ok := tool.ResourceBindings[resourceParams[j].Name]; ok {
				resourceParams[j].BindingName = override
			}
		}

		pkg.Tools[i] = tooldef.PackageTool{
			EntryTS:        tool.EntryTS,
			Idempotent:     tool.Idempotent,
			AccessMode:     accessMode,
			ResourceParams: resourceParams,
		}
	}
	return pkg
}

// ValidateCompiled validates a compiled package against both the dev (lenient)
// and dist (strict) schemas. In dev mode, dist violations are returned as
// warnings. In dist mode, they are errors.
func ValidateCompiled(pkg tooldef.Package, mode ValidationMode) ([]Warning, error) {
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

// InferAccessMode derives an access mode from the tool entry filename verb.
func InferAccessMode(entryTS string) tooldef.AccessMode {
	verb := inferVerb(entryTS)
	switch verb {
	case "list", "get", "read", "fetch", "search", "find", "describe":
		return tooldef.AccessModeReadOnly
	case "create", "add", "send", "post", "clone", "new":
		return tooldef.AccessModeAppendOnly
	case "update", "delete", "remove", "set", "put", "patch", "replace", "edit":
		return tooldef.AccessModeCanDestruct
	default:
		return tooldef.AccessModeCanDestruct
	}
}

// InferToolName derives the tool name from the entry filename.
func InferToolName(entryTS string) string {
	base := filepath.Base(entryTS)
	return strings.TrimSuffix(base, filepath.Ext(base))
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
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, ".")

	// Need at least 3 parts: resource.subresource.verb
	if len(parts) < 3 {
		return nil
	}

	verb := parts[len(parts)-1]
	resources := parts[:len(parts)-1] // all segments except verb

	// Default: include all resource IDs (member operation).
	// Collection methods exclude the deepest resource ID.
	count := len(resources)
	if isCollectionMethod(verb) {
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
	case "list", "create", "add", "search", "find", "new", "send", "post":
		return true
	default:
		return false
	}
}

// singularize naively strips a trailing 's' from a word.
func singularize(s string) string {
	if len(s) > 1 && s[len(s)-1] == 's' {
		return s[:len(s)-1]
	}
	return s
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
