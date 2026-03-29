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
	Name          string
	Description   string
	ParamsSchema  map[string]any
	Sig           *toolbox.FuncSignature
	hiddenParams  map[string]bool
	boundLiterals map[string]any // param name -> constant value for non-hidden bindings
	AccessMode    tooldef.AccessMode
	Idempotent    *bool
}

// HiddenParams returns the set of hidden param names for this tool.
func (t AgentTool) HiddenParams() map[string]bool {
	return t.hiddenParams
}

// BoundLiterals returns the map of param name to constant value for
// non-hidden bound params.
func (t AgentTool) BoundLiterals() map[string]any {
	return t.boundLiterals
}

// ParamsType returns the combined parameter type from the function signature,
// with hidden bound params removed and non-hidden bound params narrowed to
// literal types. Returns nil if no signature is available.
func (t AgentTool) ParamsType() *toolbox.TSType {
	if t.Sig == nil {
		return nil
	}
	pt := t.Sig.ParamsAsObject()
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
	for name, val := range t.boundLiterals {
		pt = pt.SetPropertyLiteral(name, val)
	}
	return pt
}

// AgentView produces the agent-visible tool surface from the resolved toolset.
// Hidden params are removed; non-hidden bound params are narrowed to literals.
func (r ResolvedToolset) AgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		hidden := r.hiddenParams[rt.Name]

		// Resolve non-hidden bindings to constant values where possible.
		var literals map[string]any
		if toolBindings := r.bindings[rt.Name]; len(toolBindings) > 0 {
			for paramName, cb := range toolBindings {
				if hidden[paramName] || cb.valueProgram == nil {
					continue
				}
				// Evaluate with empty params — if it succeeds, the binding
				// doesn't depend on agent input and is a constant.
				val, err := evalBinding(cb.valueProgram, map[string]any{}, r.context)
				if err == nil {
					if literals == nil {
						literals = make(map[string]any)
					}
					literals[paramName] = val
				}
			}
		}

		at := AgentTool{
			Name:          rt.Name,
			Description:   rt.Description,
			Sig:           rt.Sig,
			hiddenParams:  hidden,
			boundLiterals: literals,
			AccessMode:    rt.AccessMode,
			Idempotent:    rt.Idempotent,
		}

		// Derive ParamsSchema from ParamsType when available (includes
		// hidden removal + literal narrowing); fall back to raw schema.
		if pt := at.ParamsType(); pt != nil {
			at.ParamsSchema = pt.ToJSONSchema()
		} else {
			at.ParamsSchema = filterHiddenParams(rt.ParamsSchema(), hidden)
		}

		tools = append(tools, at)
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
