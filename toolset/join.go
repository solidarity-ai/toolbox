package toolset

import "fmt"

// JoinPreparedToolsets concatenates multiple prepared toolsets while
// preserving per-tool prepared state such as bindings and account metadata.
// It returns an error when duplicate tool names would collide.
func JoinPreparedToolsets(sets ...PreparedToolset) (PreparedToolset, error) {
	out := PreparedToolset{
		byName: make(map[string]int),
	}

	for _, set := range sets {
		if out.fetchTransport == nil && set.fetchTransport != nil {
			out.fetchTransport = set.fetchTransport
		}
		out.omitted = append(out.omitted, set.omitted...)
		for _, tool := range set.tools {
			if _, exists := out.byName[tool.Name]; exists {
				return PreparedToolset{}, fmt.Errorf("duplicate prepared tool %q", tool.Name)
			}
			out.byName[tool.Name] = len(out.tools)
			out.tools = append(out.tools, tool)
		}
	}

	return out, nil
}
