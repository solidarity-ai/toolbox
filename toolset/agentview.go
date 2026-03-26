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
	Sig          *toolbox.FuncSig
	hiddenParams map[string]bool
	AccessMode   tooldef.AccessMode
	Idempotent   *bool
}

// ParamsType returns the combined parameter type from the function signature,
// with hidden bound params removed. Returns nil if no signature is available.
func (t AgentTool) ParamsType() *toolbox.ParamsType {
	if t.Sig == nil {
		return nil
	}
	pt := t.Sig.CombinedParamsType()
	if pt == nil {
		return nil
	}
	if len(t.hiddenParams) > 0 {
		names := make([]string, 0, len(t.hiddenParams))
		for n := range t.hiddenParams {
			names = append(names, n)
		}
		pt = pt.RemoveProperties(names...)
	}
	return pt
}

// AgentView produces the agent-visible tool surface from the resolved toolset.
// Hidden params are removed from each tool's ParamsSchema.
func (r ResolvedToolset) AgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		hidden := r.hiddenParams[rt.Name]
		tools = append(tools, AgentTool{
			Name:         rt.Name,
			Description:  rt.Description,
			ParamsSchema: filterHiddenParams(rt.ParamsSchema(), hidden),
			Sig:          rt.Sig,
			hiddenParams: hidden,
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
