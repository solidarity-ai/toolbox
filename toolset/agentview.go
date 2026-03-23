package toolset

import tooldef "github.com/solidarity-ai/toolbox/tool"

// AgentView is the agent-visible API surface produced by Resolve.
type AgentView struct {
	Tools []AgentTool
}

// AgentTool is one tool as seen by the agent — with hidden params removed.
type AgentTool struct {
	Name        string
	Description string
	// TODO: When TS metadata extraction lands (ParamTypes/ReturnType on AgentTool),
	// consider deriving ParamsSchema from the TS types rather than carrying the
	// JSON Schema through from PackageTool. This would make AgentView the single
	// source of truth for the agent-visible type surface.
	ParamsSchema map[string]any // JSON Schema with hidden params removed
	AccessMode   tooldef.AccessMode
	Idempotent   *bool
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
			AccessMode:   rt.AccessMode,
			Idempotent:   rt.Idempotent,
		})
	}
	return AgentView{Tools: tools}
}

// filterHiddenParams returns a copy of the JSON Schema with hidden params removed.
// This only handles top-level properties, which matches the binding model's
// constraint that bindings operate on top-level params only (no nested paths).
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

	// Shallow copy the schema, then filter properties and required.
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
