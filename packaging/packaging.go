package packaging

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
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

type packageManifest struct {
	Name                      string                `json:"name"`
	Runtime                   tooldef.ToolRuntime   `json:"runtime"`
	AdditionalTypeScriptGlobs []string              `json:"additionalTypeScriptGlobs"`
	Executables               map[string]string     `json:"executables"`
	Tools                     []packageManifestTool `json:"tools"`
}

type packageManifestTool struct {
	EntryTS    string              `json:"entry_ts"`
	Idempotent *bool               `json:"idempotent"`
	AccessMode *tooldef.AccessMode `json:"accessMode"`
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

type LoadedPackage struct {
	Package tooldef.Package
	Files   fs.FS
	Dir     string
}

// LoadPackageFromDir loads a source package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.pkg.json.
func LoadPackageFromDir(dir string) (tooldef.Package, error) {
	result, err := LoadSourceDirWithMode(dir, ValidationModeDev)
	if err != nil {
		return tooldef.Package{}, err
	}
	return result.Package, nil
}

// LoadPackageFromDirWithMode loads a source package using the requested validation mode.
func LoadPackageFromDirWithMode(dir string, mode ValidationMode) (LoadResult, error) {
	return LoadSourceDirWithMode(dir, mode)
}

// LoadSourceDir loads a source package rooted at dir.
func LoadSourceDir(dir string) (tooldef.Package, error) {
	result, err := LoadSourceDirWithMode(dir, ValidationModeDev)
	if err != nil {
		return tooldef.Package{}, err
	}
	return result.Package, nil
}

// LoadSourceDirWithMode loads a source package and validates its compiled form.
func LoadSourceDirWithMode(dir string, mode ValidationMode) (LoadResult, error) {
	manifestPath := filepath.Join(dir, "toolbox.pkg.json")
	return loadSourcePackageFromFile(manifestPath, mode)
}

func LoadSourcePackage(dir string) (LoadedPackage, error) {
	return LoadSourcePackageWithMode(dir, ValidationModeDev)
}

func LoadSourcePackageWithMode(dir string, mode ValidationMode) (LoadedPackage, error) {
	result, err := LoadSourceDirWithMode(dir, mode)
	if err != nil {
		return LoadedPackage{}, err
	}
	return LoadedPackage{
		Package: result.Package,
		Files:   newSourceFS(os.DirFS(dir), dir, result.Package),
		Dir:     dir,
	}, nil
}

// LoadBuiltDir loads a compiled package rooted at dir.
func LoadBuiltDir(dir string) (tooldef.Package, error) {
	result, err := LoadBuiltDirWithMode(dir, ValidationModeDev)
	if err != nil {
		return tooldef.Package{}, err
	}
	return result.Package, nil
}

// LoadBuiltDirWithMode loads a compiled package using the requested validation mode.
func LoadBuiltDirWithMode(dir string, mode ValidationMode) (LoadResult, error) {
	compiledPath := filepath.Join(dir, "toolbox.pkg.compiled.json")
	return loadBuiltPackageFromFile(compiledPath, mode)
}

func LoadBuiltPackage(dir string) (LoadedPackage, error) {
	return LoadBuiltPackageWithMode(dir, ValidationModeDev)
}

func LoadBuiltPackageWithMode(dir string, mode ValidationMode) (LoadedPackage, error) {
	result, err := LoadBuiltDirWithMode(dir, mode)
	if err != nil {
		return LoadedPackage{}, err
	}
	return LoadedPackage{
		Package: result.Package,
		Files:   newSourceFS(os.DirFS(dir), dir, result.Package),
		Dir:     dir,
	}, nil
}

func loadSourcePackageFromFile(manifestPath string, mode ValidationMode) (LoadResult, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return LoadResult{}, err
	}

	var sourceInstance map[string]any
	if err := json.Unmarshal(raw, &sourceInstance); err != nil {
		return LoadResult{}, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	if err := resolvedToolboxPkgDevSchema.Validate(sourceInstance); err != nil {
		return LoadResult{}, fmt.Errorf("validate source package %s: %w", manifestPath, err)
	}

	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return LoadResult{}, fmt.Errorf("read %s: %w", manifestPath, err)
	}

	pkg := compilePackage(manifest)

	warnings, err := validateCompiledPackage(pkg, mode, manifestPath)
	if err != nil {
		return LoadResult{}, err
	}

	return LoadResult{
		Package:  pkg,
		Warnings: warnings,
	}, nil
}

func loadBuiltPackageFromFile(compiledPath string, mode ValidationMode) (LoadResult, error) {
	raw, err := os.ReadFile(compiledPath)
	if err != nil {
		return LoadResult{}, err
	}

	var pkg tooldef.Package
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return LoadResult{}, fmt.Errorf("read %s: %w", compiledPath, err)
	}

	warnings, err := validateCompiledPackage(pkg, mode, compiledPath)
	if err != nil {
		return LoadResult{}, err
	}

	return LoadResult{
		Package:  pkg,
		Warnings: warnings,
	}, nil
}

func compilePackage(manifest packageManifest) tooldef.Package {
	pkg := tooldef.Package{
		Name:                      manifest.Name,
		Runtime:                   manifest.Runtime,
		AdditionalTypeScriptGlobs: append([]string(nil), manifest.AdditionalTypeScriptGlobs...),
		Tools:                     make([]tooldef.PackageTool, len(manifest.Tools)),
	}
	for i, tool := range manifest.Tools {
		accessMode := inferAccessMode(tool.EntryTS)
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

func validateCompiledPackage(pkg tooldef.Package, mode ValidationMode, label string) ([]Warning, error) {
	raw, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("marshal compiled package %s: %w", label, err)
	}

	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return nil, fmt.Errorf("unmarshal compiled package %s: %w", label, err)
	}
	if err := resolvedToolboxPkgDevSchema.Validate(instance); err != nil {
		return nil, fmt.Errorf("validate compiled package %s: %w", label, err)
	}

	var warnings []Warning
	if err := resolvedToolboxPkgDistSchema.Validate(instance); err != nil {
		if mode == ValidationModeDist {
			return nil, fmt.Errorf("validate compiled package %s for distribution: %w", label, err)
		}
		warnings = append(warnings, Warning{Message: fmt.Sprintf("distribution validation: %v", err)})
	}
	return warnings, nil
}

func inferAccessMode(entryTS string) tooldef.AccessMode {
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

func inferVerb(entryTS string) string {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (p LoadedPackage) ResolvedTools() []tooldef.ResolvedTool {
	tools := make([]tooldef.ResolvedTool, 0, len(p.Package.Tools))
	var manifest packageManifest
	if p.Package.Runtime == tooldef.RuntimeTypeScriptWasixCLI {
		raw, err := os.ReadFile(filepath.Join(p.Dir, "toolbox.pkg.json"))
		if err != nil {
			panic(fmt.Errorf("read toolbox.pkg.json for %s: %w", p.Dir, err))
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			panic(fmt.Errorf("read toolbox.pkg.json for %s: %w", p.Dir, err))
		}
	}
	for _, pkgTool := range p.Package.Tools {
		resolved := tooldef.ResolvedTool{
			Name:        inferToolName(pkgTool.EntryTS),
			Description: inferToolDescription(pkgTool.EntryTS),
			Package:     &p.Package,
		}

		baseDef := tooldef.TSToolDef{
			Entry:       pkgTool.EntryTS,
			Files:       p.Files,
			PackageRoot: p.Dir,
		}

		if p.Package.Runtime == tooldef.RuntimeTypeScriptWasixCLI {
			resolved.TSWasm = &tooldef.TSWasmToolDef{
				TSToolDef:   baseDef,
				Executables: manifest.Executables,
			}
		} else {
			resolved.TS = &baseDef
		}

		tools = append(tools, resolved)
	}
	return tools
}

func inferToolName(entryTS string) string {
	base := filepath.Base(entryTS)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func inferToolDescription(entryTS string) string {
	return inferToolName(entryTS)
}
