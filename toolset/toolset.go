package toolset

// Tool is the smallest useful agent-visible tool shape for the first outside-in seam.
//
// More fields can be added later when the real resolved toolset needs them.
type Tool struct {
	Name        string
	Description string
}

// ResolvedToolset is a stubbed resolved toolset for the first outside-in tests.
//
// For now it only carries the visible tools for one request.
type ResolvedToolset struct {
	tools []Tool
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
