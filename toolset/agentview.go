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
	Effect        tooldef.Effect
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

// AgentView produces the agent-visible tool surface from the prepared toolset.
// Hidden params are removed; non-hidden bound params are narrowed to literals.
func (r PreparedToolset) AgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		at := AgentTool{
			Name:          rt.Name,
			Description:   rt.Description,
			Sig:           rt.Sig,
			hiddenParams:  rt.HiddenParams(),
			boundLiterals: rt.boundLiterals(),
			Effect:        rt.Effect,
			Idempotent:    rt.Idempotent,
		}

		// Inject account selection params into the Sig when available.
		if len(rt.accountParams) > 0 && at.Sig != nil {
			sig := at.Sig
			for _, ap := range rt.accountParams {
				unionType := toolbox.NewStringLiteralUnion(ap.Accounts)
				sig = sig.AddParam(ap.ParamName, unionType, ap.Description, false)
			}
			at.Sig = sig
		}

		// Derive ParamsSchema from ParamsType (includes hidden removal,
		// literal narrowing, and account params from the augmented Sig).
		if pt := at.ParamsType(); pt != nil {
			at.ParamsSchema = pt.ToJSONSchema()
		}

		tools = append(tools, at)
	}
	return AgentView{Tools: tools}
}
