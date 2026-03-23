package toolset

import tooldef "github.com/solidarity-ai/toolbox/tool"

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
	ParamTypes   []ParamType
	ReturnType   string
	ReadOnly     bool
	Idempotent   bool
}

// ParamType mirrors the typescript-go extraction output for one parameter.
type ParamType struct {
	Name     string
	TypeText string
	Optional bool
}

// AgentView produces the agent-visible tool surface from a resolved toolset.
// Hidden params are removed from schemas. Visible params retain their metadata.
func (r ResolvedToolset) AgentView() AgentView {
	var tools []AgentTool
	for _, rt := range r.tools {
		at := AgentTool{
			Name:         rt.Name,
			Description:  rt.Description,
			ParamsSchema: filterSchema(rt.ParamsSchema, r.hiddenParams[rt.Name]),
			ReadOnly:     rt.Package != nil && rt.Package.Tools != nil && isReadOnly(rt),
			Idempotent:   isIdempotent(rt),
		}
		tools = append(tools, at)
	}
	return AgentView{Tools: tools}
}

// filterSchema returns a copy of schema with hidden params removed from
// "properties" and "required".
func filterSchema(schema map[string]any, hidden map[string]bool) map[string]any {
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

func isReadOnly(rt tooldef.ResolvedTool) bool {
	if rt.Package == nil {
		return false
	}
	for _, pt := range rt.Package.Tools {
		if rt.TS != nil && pt.EntryTS == rt.TS.Entry {
			return pt.AccessMode == tooldef.AccessModeReadOnly
		}
		if rt.TSWasm != nil && pt.EntryTS == rt.TSWasm.Entry {
			return pt.AccessMode == tooldef.AccessModeReadOnly
		}
	}
	return false
}

func isIdempotent(rt tooldef.ResolvedTool) bool {
	if rt.Package == nil {
		return false
	}
	for _, pt := range rt.Package.Tools {
		if rt.TS != nil && pt.EntryTS == rt.TS.Entry {
			return pt.Idempotent != nil && *pt.Idempotent
		}
		if rt.TSWasm != nil && pt.EntryTS == rt.TSWasm.Entry {
			return pt.Idempotent != nil && *pt.Idempotent
		}
	}
	return false
}
