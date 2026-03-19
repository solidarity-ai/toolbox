package packaging

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/jsonschema-go/jsonschema"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

//go:embed toolbox_pkg.dev.schema.json
var toolboxPkgDevSchemaJSON []byte

//go:embed toolbox_pkg.dist.schema.json
var toolboxPkgDistSchemaJSON []byte

var resolvedToolboxPkgDevSchema = mustResolveSchema(toolboxPkgDevSchemaJSON)
var resolvedToolboxPkgDistSchema = mustResolveSchema(toolboxPkgDistSchemaJSON)

type packageManifest struct {
	Name    string                `json:"name"`
	Runtime tooldef.ToolRuntime   `json:"runtime"`
	Tools   []packageManifestTool `json:"tools"`
}

type packageManifestTool struct {
	EntryTS    string `json:"entry_ts"`
	Idempotent *bool  `json:"idempotent"`
}

type ValidationMode int

const (
	ValidationModeDev ValidationMode = iota
	ValidationModeDist
)

func mustResolveSchema(raw []byte) *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		panic(fmt.Errorf("packaging: unmarshal embedded package schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("packaging: resolve embedded package schema: %w", err))
	}
	return resolved
}

type Warning struct {
	Message string
}

type LoadResult struct {
	Package  tooldef.Package
	Warnings []Warning
}

// LoadPackageFromDir loads a source package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.pkg.json.
func LoadPackageFromDir(dir string) (tooldef.Package, error) {
	result, err := LoadPackageFromDirWithMode(dir, ValidationModeDev)
	if err != nil {
		return tooldef.Package{}, err
	}
	return result.Package, nil
}

// LoadPackageFromDirWithMode loads a source package using the requested validation mode.
func LoadPackageFromDirWithMode(dir string, mode ValidationMode) (LoadResult, error) {
	manifestPath := filepath.Join(dir, "toolbox.pkg.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return LoadResult{}, err
	}

	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return LoadResult{}, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	if err := resolvedToolboxPkgDevSchema.Validate(instance); err != nil {
		return LoadResult{}, fmt.Errorf("validate %s: %w", manifestPath, err)
	}
	var warnings []Warning
	if err := resolvedToolboxPkgDistSchema.Validate(instance); err != nil {
		if mode == ValidationModeDist {
			return LoadResult{}, fmt.Errorf("validate %s for distribution: %w", manifestPath, err)
		}
		warnings = append(warnings, Warning{Message: fmt.Sprintf("distribution validation: %v", err)})
	}

	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return LoadResult{}, fmt.Errorf("read %s: %w", manifestPath, err)
	}

	pkg := tooldef.Package{
		Name:    manifest.Name,
		Runtime: manifest.Runtime,
		Tools:   make([]tooldef.PackageTool, len(manifest.Tools)),
	}
	for i, tool := range manifest.Tools {
		pkg.Tools[i] = tooldef.PackageTool{
			EntryTS:    tool.EntryTS,
			Idempotent: tool.Idempotent,
		}
	}

	return LoadResult{
		Package:  pkg,
		Warnings: warnings,
	}, nil
}
