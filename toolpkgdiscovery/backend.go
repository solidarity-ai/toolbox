package toolpkgdiscovery

import (
	"context"

	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// Backend describes the read-only package discovery surface that can be
// exposed alongside a prepared toolset.
type Backend interface {
	// EnableToolsForPackageDiscovery reports whether read-only package discovery
	// tools such as search and inspect are enabled for the current backend
	// snapshot. Callers use this for instruction shaping and similar guidance.
	EnableToolsForPackageDiscovery() bool
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
	Inspect(ctx context.Context, req InspectRequest) (InspectResult, error)
}

// SearchRequest describes a package or tool search against the registry-backed
// package ecosystem.
type SearchRequest struct {
	Query    string
	Tools    bool
	Packages bool
	Runtime  string
	Effect   string
	Limit    int
	Offset   int
}

// SearchResult carries either package hits or tool hits, depending on the
// requested search mode.
type SearchResult struct {
	Packages []registry.PackageSearchHit
	Tools    []registry.ToolSearchHit
}

// InspectRequest asks the backend to describe one package target.
type InspectRequest struct {
	Target string
}

// InspectResult describes one resolved or installed package target.
type InspectResult struct {
	Target  string
	Version string
	Source  string
	Package tooldef.Package
}
