package toolset

import (
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// Tool is the smallest useful agent-visible tool shape for the first outside-in seam.
//
// More fields can be added later when the real resolved toolset needs them.
type Tool struct {
	Name        string
	Description string
	TS          *tooldef.TSToolDef
}

// ResolvedToolset is a stubbed resolved toolset for the first outside-in tests.
//
// For now it only carries the visible tools for one request.
type ResolvedToolset struct {
	tools []Tool
}

// Builder incrementally assembles a toolset from source package directories.
//
// For now it only records loaded packages from
// toolbox.pkg.json. Tool selection and binding come later.
type Builder struct {
	packages []tooldef.Package
}

// New creates an empty toolset builder.
func New() *Builder {
	return &Builder{}
}

// AddFromDir loads a package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.pkg.json.
func (b *Builder) AddFromDir(dir string) error {
	pkg, err := packaging.LoadSourceDir(dir)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// Packages returns the currently loaded source packages.
func (b *Builder) Packages() []tooldef.Package {
	out := make([]tooldef.Package, len(b.packages))
	copy(out, b.packages)
	return out
}

// NewResolvedToolset creates a resolved toolset from a visible tool list.
func NewResolvedToolset(tools []Tool) ResolvedToolset {
	out := make([]Tool, len(tools))
	copy(out, tools)
	return ResolvedToolset{tools: out}
}

// Tools returns a shallow copy of the visible tools for this resolved toolset.
func (r ResolvedToolset) Tools() []Tool {
	out := make([]Tool, len(r.tools))
	copy(out, r.tools)
	return out
}
