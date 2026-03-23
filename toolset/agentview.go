package toolset

// AgentView is the agent-visible API surface produced by Resolve.
type AgentView struct {
	Tools []AgentTool
}

// AgentTool is one tool as seen by the agent — with hidden params removed.
type AgentTool struct {
	Name         string
	Description  string
	ParamsSchema map[string]any // JSON Schema with hidden params removed
	ReadOnly     bool
	Idempotent   bool
}

// AgentView produces the agent-visible tool surface from the resolved toolset.
// Hidden params are removed from each tool's ParamsSchema.
func (r ResolvedToolset) AgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		schema := rt.ParamsSchema
		if bindings, ok := r.bindings[rt.Name]; ok {
			schema = filterHiddenParams(schema, bindings)
		}

		tools = append(tools, AgentTool{
			Name:         rt.Name,
			Description:  rt.Description,
			ParamsSchema: schema,
		})
	}
	return AgentView{Tools: tools}
}

// filterHiddenParams returns a copy of the JSON Schema with hidden params removed.
func filterHiddenParams(schema map[string]any, bindings map[string]resolvedBinding) map[string]any {
	if schema == nil {
		return nil
	}

	// Find which params are hidden.
	hidden := map[string]bool{}
	for name, b := range bindings {
		if b.hidden {
			hidden[name] = true
		}
	}
	if len(hidden) == 0 {
		return schema
	}

	// Deep copy the schema and remove hidden params.
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}

	if props, ok := out["properties"].(map[string]any); ok {
		filtered := make(map[string]any, len(props))
		for k, v := range props {
			if !hidden[k] {
				filtered[k] = v
			}
		}
		out["properties"] = filtered
	}

	if required, ok := out["required"].([]any); ok {
		filtered := make([]any, 0, len(required))
		for _, v := range required {
			if name, ok := v.(string); ok && !hidden[name] {
				filtered = append(filtered, v)
			}
		}
		out["required"] = filtered
	}

	return out
}
