package toolset

import "fmt"

// ValidateCall evaluates bindings against agent-provided params and context.
// It returns the full param set (visible + hidden injected) ready for runtime dispatch.
// If a check expression fails, it returns a policy violation error.
func (r ResolvedToolset) ValidateCall(toolName string, agentParams map[string]any) (map[string]any, error) {
	// Verify the tool exists.
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

	paramBindings, hasBindings := r.bindings[toolName]
	if !hasBindings {
		// No bindings — pass through agent params as-is.
		out := make(map[string]any, len(agentParams))
		for k, v := range agentParams {
			out[k] = v
		}
		return out, nil
	}

	// Start with agent-provided visible params.
	fullParams := make(map[string]any, len(agentParams)+len(paramBindings))
	for k, v := range agentParams {
		fullParams[k] = v
	}

	// Evaluate each binding.
	for paramName, rb := range paramBindings {
		// Evaluate check expression first.
		if rb.check != nil {
			pass, err := evalCheck(rb.check, agentParams, r.context)
			if err != nil {
				return nil, fmt.Errorf("tool %q param %q: %w", toolName, paramName, err)
			}
			if !pass {
				return nil, fmt.Errorf("tool %q param %q: check failed", toolName, paramName)
			}
		}

		// Evaluate value binding.
		if rb.value != nil {
			val, err := evalBinding(rb.value, agentParams, r.context)
			if err != nil {
				return nil, fmt.Errorf("tool %q param %q: %w", toolName, paramName, err)
			}
			fullParams[paramName] = val
		}
	}

	return fullParams, nil
}
