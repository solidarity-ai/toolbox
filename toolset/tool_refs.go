package toolset

import (
	"sort"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// PreparedToolRefs returns stable fully-qualified tool refs for the prepared
// snapshot, including package versions when available.
func PreparedToolRefs(prepared PreparedToolset) []string {
	tools := prepared.Tools()
	if len(tools) == 0 {
		return nil
	}

	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		switch {
		case tool.PackageMeta != nil && tool.PackageVersion != "":
			out = append(out, tooldef.ToolFQN{
				Module:  tool.PackageMeta.Module,
				Version: tool.PackageVersion,
				Tool:    tooldef.ToolPath(tool.Name),
			}.String())
		case tool.PackageMeta != nil:
			out = append(out, tool.PackageMeta.Module.String()+"/"+tool.Name)
		default:
			out = append(out, tool.Name)
		}
	}
	sort.Strings(out)
	return out
}
