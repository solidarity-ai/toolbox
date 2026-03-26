package toolset

import (
	"github.com/microsoft/typescript-go/toolbox"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// AgentView is the agent-visible API surface produced by resolving a toolset
// with bindings. Hidden params are stripped; tool names may be shortened.
type AgentView struct {
	Tools []AgentTool
}

// AgentTool is a single tool visible to the agent.
type AgentTool struct {
	Name         string
	Description  string
	ParamsSchema map[string]any
	ParamsType *toolbox.ParamsType
	Sig      *toolbox.FuncSig
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
			ParamsType: filterHiddenParamsType(rt.ParamsType, r.hiddenParams[rt.Name]),
			Sig:      rt.Sig,
			AccessMode:   rt.AccessMode,
			Idempotent:   rt.Idempotent,
		})
	}
	return AgentView{Tools: tools}
}

// filterHiddenParamsType returns a copy of the ParamsType with hidden properties removed.
// This only handles top-level properties, matching the binding model constraint.
func filterHiddenParamsType(t *toolbox.ParamsType, hidden map[string]bool) *toolbox.ParamsType {
	if len(hidden) == 0 || t == nil {
		return t
	}
	names := make([]string, 0, len(hidden))
	for n := range hidden {
		names = append(names, n)
	}
	return t.RemoveProperties(names...)
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
