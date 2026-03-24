package toolset

import tooldef "github.com/solidarity-ai/toolbox/tool"

// AgentView is the agent-visible API surface produced by resolving a toolset
// with bindings. Hidden params are stripped; tool names may be shortened.
type AgentView struct {
	Tools []AgentTool
}

// AgentTool is a single tool visible to the agent.
type AgentTool struct {
	Name        string
	Description string
	// TODO: When TS metadata extraction lands (ParamTypes/ReturnType on AgentTool),
	// consider deriving ParamsSchema from the TS types rather than carrying the
	// JSON Schema through from PackageTool. This would make AgentView the single
	// source of truth for the agent-visible type surface.
	ParamsSchema map[string]any
	AccessMode   tooldef.AccessMode
	Idempotent   *bool
}

// AgentView produces the agent-visible tool surface from the resolved toolset.
// Hidden params are removed from each tool's ParamsSchema.
func (r ResolvedToolset) AgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		tools = append(tools, AgentTool{
			Name:         rt.Name,
			Description:  rt.Description,
			ParamsSchema: filterHiddenParams(rt.ParamsSchema, r.hiddenParams[rt.Name]),
			AccessMode:   rt.AccessMode,
			Idempotent:   rt.Idempotent,
		})
	}
	return AgentView{Tools: tools}
}

// filterHiddenParams returns a copy of the JSON Schema with hidden params removed.
// This only handles top-level properties, which matches the binding model's
// constraint that bindings operate on top-level params only (no nested paths).
func filterHiddenParams(schema map[string]any, hidden map[string]bool) map[string]any {
	if len(hidden) == 0 || schema == nil {
		return schema
	}

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
		var filtered []any
		for _, r := range required {
			name, ok := r.(string)
			if !ok || !hidden[name] {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) > 0 {
			out["required"] = filtered
		} else {
			delete(out, "required")
		}
	}

	return out
}
