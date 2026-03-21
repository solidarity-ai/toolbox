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
	EntryTS    string              `json:"entry_ts"`
	Idempotent *bool               `json:"idempotent"`
	AccessMode *tooldef.AccessMode `json:"accessMode"`
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
		Tools:                     make([]tooldef.PackageTool, len(dev.Tools)),
	}
	for i, tool := range dev.Tools {
		accessMode := InferAccessMode(tool.EntryTS)
		if tool.AccessMode != nil {
			accessMode = *tool.AccessMode
		}
		pkg.Tools[i] = tooldef.PackageTool{
			EntryTS:    tool.EntryTS,
			Idempotent: tool.Idempotent,
			AccessMode: accessMode,
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

func inferVerb(entryTS string) string {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
