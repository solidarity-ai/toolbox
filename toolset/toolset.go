package toolset

import (
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// ResolvedToolset is a stubbed resolved toolset for the first outside-in tests.
//
// For now it only carries the visible tools for one request.
type ResolvedToolset struct {
	tools []tooldef.ResolvedTool
}

// Builder incrementally assembles a toolset from source package directories.
//
// For now it only records loaded packages from
// toolbox.pkg.json. Tool selection and binding come later.
type Builder struct {
	packages []packaging.LoadedPackage
}

// New creates an empty toolset builder.
func New() *Builder {
	return &Builder{}
}

// AddFromDir loads a package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.pkg.json.
func (b *Builder) AddFromDir(dir string) error {
	pkg, err := packaging.LoadSourcePackage(dir)
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

// Resolve materializes one visible tool per loaded package tool.
func (b *Builder) Resolve() ResolvedToolset {
	var tools []tooldef.ResolvedTool
	for _, loaded := range b.packages {
		tools = append(tools, loaded.ResolvedTools()...)
	}
	return NewResolvedToolset(tools)
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
