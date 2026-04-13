package toolset

import "fmt"

// ValidateCall evaluates bindings against agent-provided params and context.
// It returns the full param set (hidden + visible, all applied) ready for
// runtime dispatch. Returns an error if a check expression fails or the tool
// is not found.
func (r PreparedToolset) ValidateCall(toolName string, agentParams map[string]any) (map[string]any, error) {
	tool, ok := r.Tool(toolName)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return tool.ValidateCall(agentParams)
}
