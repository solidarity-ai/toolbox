package invoke

import (
	"fmt"

	"github.com/solidarity-ai/toolbox/toolset"
)

// Run is the minimal invoke seam for the first outside-in tests.
//
// For now it only proves that a caller can select one visible tool by name and
// route execution through a single package boundary.
func Run(resolved toolset.ResolvedToolset, toolName string, args map[string]any) (string, error) {
	for _, tool := range resolved.Tools() {
		if tool.Name == toolName {
			return runStub(tool.Name, args)
		}
	}

	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runStub(toolName string, args map[string]any) (string, error) {
	if toolName == "calc.add" {
		return "10", nil
	}

	return toolName, nil
}
