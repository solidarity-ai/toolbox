package toolset

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// resolvedBinding holds a compiled CEL binding for one parameter.
type resolvedBinding struct {
	value  cel.Program // compiled Value expression (nil if no Value)
	check  cel.Program // compiled Check expression (nil if no Check)
	hidden bool
}

// ResolvedToolset carries the visible tools and their compiled bindings.
type ResolvedToolset struct {
	tools    []tooldef.ResolvedTool
	bindings map[string]map[string]resolvedBinding // tool name -> param name -> binding
	context  map[string]any
}

// Builder incrementally assembles a toolset from source package directories.
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

// Resolve materializes the toolset with the given binding configuration.
// Passing Config{} produces the same result as an unbound resolve.
func (b *Builder) Resolve(cfg Config) (ResolvedToolset, error) {
	var tools []tooldef.ResolvedTool
	for _, loaded := range b.packages {
		tools = append(tools, loaded.ResolvedTools()...)
	}
	return ResolveTools(tools, cfg)
}

// ResolveTools resolves a pre-built list of tools with the given config.
// This is useful for testing with synthetic tool definitions.
func ResolveTools(tools []tooldef.ResolvedTool, cfg Config) (ResolvedToolset, error) {
	// Merge per-tool bindings from Config.Tools.
	toolBindings := make(map[string]map[string]Binding)
	for _, bt := range cfg.Tools {
		toolBindings[bt.ToolRef] = bt.Bindings
	}

	// Propagate resource-level bindings to tools via canonical binding names.
	for _, tool := range tools {
		for _, rp := range tool.ResourceParams {
			rb, ok := cfg.ResourceBindings[rp.BindingName]
			if !ok {
				continue
			}
			if toolBindings[tool.Name] == nil {
				toolBindings[tool.Name] = make(map[string]Binding)
			}
			// Per-tool bindings take precedence over resource bindings.
			if _, exists := toolBindings[tool.Name][rp.Name]; !exists {
				toolBindings[tool.Name][rp.Name] = rb
			}
		}
	}

	// Compile all CEL expressions at resolve time.
	env, err := newCELEnv()
	if err != nil {
		return ResolvedToolset{}, fmt.Errorf("create CEL environment: %w", err)
	}

	compiled := make(map[string]map[string]resolvedBinding)
	for toolName, paramBindings := range toolBindings {
		compiledParams := make(map[string]resolvedBinding)
		for paramName, binding := range paramBindings {
			rb, err := compileResolvedBinding(env, binding)
			if err != nil {
				return ResolvedToolset{}, fmt.Errorf("tool %q param %q: %w", toolName, paramName, err)
			}
			compiledParams[paramName] = rb
		}
		compiled[toolName] = compiledParams
	}

	ctx := cfg.Context
	if ctx == nil {
		ctx = map[string]any{}
	}

	return ResolvedToolset{
		tools:    tools,
		bindings: compiled,
		context:  ctx,
	}, nil
}

// compileResolvedBinding compiles Value and Check CEL expressions from a Binding.
func compileResolvedBinding(env *cel.Env, binding Binding) (resolvedBinding, error) {
	rb := resolvedBinding{hidden: binding.Hidden}

	if binding.Value != "" {
		prog, err := compileBinding(env, binding.Value)
		if err != nil {
			return resolvedBinding{}, fmt.Errorf("value: %w", err)
		}
		rb.value = prog
	}

	if binding.Check != "" {
		prog, err := compileBinding(env, binding.Check)
		if err != nil {
			return resolvedBinding{}, fmt.Errorf("check: %w", err)
		}
		rb.check = prog
	}

	return rb, nil
}

// NewResolvedToolset creates a resolved toolset from a visible tool list.
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
