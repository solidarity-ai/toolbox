package toolset

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// ResolvedToolset carries the visible tools and their compiled bindings.
type ResolvedToolset struct {
	tools        []tooldef.ResolvedTool
	bindings     map[string]map[string]compiledBinding // tool name -> param name -> compiled binding
	hiddenParams map[string]map[string]bool            // tool name -> set of hidden param names
	context      map[string]any
	celEnv       *cel.Env
}

// Builder incrementally assembles a toolset from source package directories.
//
// For now it only records loaded packages from
// toolbox.devpkg.json. Tool selection and binding come later.
type Builder struct {
	packages []packaging.LoadedPackage
}

// New creates an empty toolset builder.
func New() *Builder {
	return &Builder{}
}

// AddFromDir loads a package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.devpkg.json.
func (b *Builder) AddFromDir(dir string) error {
	pkg, err := packaging.LoadDev(dir)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// AddFromArchive loads a package from a .toolbox.pkg archive and its manifest.
func (b *Builder) AddFromArchive(archivePath, manifestPath string) error {
	pkg, err := packaging.LoadArchive(archivePath, manifestPath)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// Packages returns the currently loaded source packages.
func (b *Builder) Packages() []tooldef.Package {
	out := make([]tooldef.Package, len(b.packages))
	for i, loaded := range b.packages {
		out[i] = loaded.Package
	}
	return out
}

// Resolve materializes visible tools from loaded packages, compiling any
// bindings from cfg. An empty Config{} produces the same result as before
// bindings existed — all tools visible, no bindings applied.
func (b *Builder) Resolve(cfg Config) (ResolvedToolset, error) {
	var tools []tooldef.ResolvedTool
	for _, loaded := range b.packages {
		tools = append(tools, loaded.ResolvedTools()...)
	}

	// Build binding lookup: tool ref -> param name -> Binding
	toolBindings := make(map[string]map[string]Binding, len(cfg.Tools))
	for _, bt := range cfg.Tools {
		toolBindings[bt.ToolRef] = bt.Bindings
	}

	env, err := newCELEnv()
	if err != nil {
		return ResolvedToolset{}, fmt.Errorf("create CEL env: %w", err)
	}

	allCompiled := make(map[string]map[string]compiledBinding, len(toolBindings))
	allHidden := make(map[string]map[string]bool)

	for _, tool := range tools {
		// Start with explicit per-tool bindings
		bindings := make(map[string]Binding)
		if tb, ok := toolBindings[tool.Name]; ok {
			for k, v := range tb {
				bindings[k] = v
			}
		}

		// Merge resource-level bindings from the two-tier model:
		// PackageTool.ResourceParams maps param name -> canonical binding name
		// Config.ResourceBindings maps canonical name -> Binding
		if tool.Package != nil {
			for _, pt := range tool.Package.Tools {
				entryMatch := (tool.TS != nil && pt.EntryTS == tool.TS.Entry) ||
					(tool.TSWasm != nil && pt.EntryTS == tool.TSWasm.Entry)
				if !entryMatch {
					continue
				}
				for _, rp := range pt.ResourceParams {
					// Skip if explicit per-tool binding already set
					if _, exists := bindings[rp.Name]; exists {
						continue
					}
					if rb, ok := cfg.ResourceBindings[rp.BindingName]; ok {
						bindings[rp.Name] = rb
					}
				}
			}
		}

		if len(bindings) == 0 {
			continue
		}

		compiled, err := compileBindings(env, bindings)
		if err != nil {
			return ResolvedToolset{}, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		allCompiled[tool.Name] = compiled

		hidden := make(map[string]bool)
		for paramName, binding := range bindings {
			if binding.Hidden {
				hidden[paramName] = true
			}
		}
		if len(hidden) > 0 {
			allHidden[tool.Name] = hidden
		}
	}

	out := make([]tooldef.ResolvedTool, len(tools))
	copy(out, tools)
	return ResolvedToolset{
		tools:        out,
		bindings:     allCompiled,
		hiddenParams: allHidden,
		context:      cfg.Context,
		celEnv:       env,
	}, nil
}

// NewResolvedToolset creates a resolved toolset from a visible tool list
// with no bindings. This is a convenience for callers that don't use bindings.
func NewResolvedToolset(tools []tooldef.ResolvedTool) ResolvedToolset {
	out := make([]tooldef.ResolvedTool, len(tools))
	copy(out, tools)
	return ResolvedToolset{tools: out}
}

// Tools returns a shallow copy of the visible tools for this resolved toolset.
func (r ResolvedToolset) Tools() []tooldef.ResolvedTool {
	out := make([]tooldef.ResolvedTool, len(r.tools))
	copy(out, r.tools)
	return out
}
