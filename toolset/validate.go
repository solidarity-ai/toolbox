package toolset

import "fmt"

// ValidateCall evaluates bindings against agent-provided params and context.
// It returns the full param set (hidden + visible, all resolved) ready for
// runtime dispatch. Returns an error if a check expression fails or the tool
// is not found.
func (r ResolvedToolset) ValidateCall(toolName string, agentParams map[string]any) (map[string]any, error) {
	// Verify tool exists
	found := false
	for _, tool := range r.tools {
		if tool.Name == toolName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	bindings := r.bindings[toolName]
	if len(bindings) == 0 {
		// No bindings — pass through agent params unchanged
		return agentParams, nil
	}

	ctx := r.context
	if ctx == nil {
		ctx = map[string]any{}
	}

	// Start with agent-provided params
	fullParams := make(map[string]any, len(agentParams)+len(bindings))
	for k, v := range agentParams {
		fullParams[k] = v
	}

	// Evaluate each binding: checks first, then values.
	// Check expressions run against agent-provided params (before injection)
	// to validate what the agent actually sent.
	for paramName, cb := range bindings {
		if cb.checkProgram != nil {
			pass, err := evalCheck(cb.checkProgram, agentParams, ctx)
			if err != nil {
				return nil, fmt.Errorf("param %q: %w", paramName, err)
			}
			if !pass {
				return nil, fmt.Errorf("check failed for param %q on tool %q", paramName, toolName)
			}
		}
	}

	// Evaluate value bindings after all checks pass.
	for paramName, cb := range bindings {
		if cb.valueProgram != nil {
			val, err := evalBinding(cb.valueProgram, agentParams, ctx)
			if err != nil {
				return nil, fmt.Errorf("param %q: %w", paramName, err)
			}
			fullParams[paramName] = val
		}
	}

	return fullParams, nil
}
