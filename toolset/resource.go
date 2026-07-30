package toolset

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/toolbox"
)

func validatePreparedResourceSurfaces(tools []PreparedTool) error {
	seen := map[string][]string{}
	for _, tool := range tools {
		hidden := tool.HiddenParams()
		params := map[string]*toolbox.FuncParam{}
		if tool.Sig != nil {
			for _, param := range tool.Sig.Params() {
				param := param
				params[param.Name()] = &param
			}
		}
		packageKey := tool.ToolApprovalPackageName()
		for _, use := range tool.ResourceUses {
			if !use.Selected {
				continue
			}
			projection := make([]string, 0, len(use.Resource.Params))
			for _, resourceParam := range use.Resource.Params {
				if hidden[resourceParam.Name] {
					continue
				}
				param := params[resourceParam.Name]
				projection = append(projection, resourceParam.Name+":"+param.Type().ToTS())
			}
			key := packageKey + "\x00" + use.Resource.Path
			if previous, ok := seen[key]; ok && !slices.Equal(previous, projection) {
				return fmt.Errorf("resource %q has incompatible visible selector parameters %s and %s", use.Resource.Path, strings.Join(previous, ", "), strings.Join(projection, ", "))
			}
			seen[key] = projection
		}
	}
	return nil
}
