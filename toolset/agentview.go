package toolset

import (
	"strings"

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
	Name               string
	Description        string
	ResourceUses       []tooldef.ResourceUse
	ParamsSchema       map[string]any
	Sig                *toolbox.FuncSignature
	paramsType         *toolbox.TSType
	hiddenParams       map[string]bool
	boundLiterals      map[string]any // param name -> constant value for non-hidden bindings
	Effect             tooldef.Effect
	Idempotent         *bool
	UnavailableReason  ToolUnavailableReason
	UnavailableMessage string
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
	return t.paramsType
}

func (t AgentTool) deriveParamsType() *toolbox.TSType {
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
func (r PreparedToolset) AgentView() AgentView {
	return cloneAgentView(r.agentView)
}

// buildAgentView constructs the AgentView once during toolset preparation.
func (r PreparedToolset) buildAgentView() AgentView {
	tools := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		at := AgentTool{
			Name:               rt.Name,
			Description:        agentToolDescription(rt),
			ResourceUses:       tooldef.CloneResourceUses(rt.ResourceUses),
			Sig:                rt.Sig,
			hiddenParams:       rt.HiddenParams(),
			boundLiterals:      rt.boundLiterals(),
			Effect:             rt.Effect,
			Idempotent:         rt.Idempotent,
			UnavailableReason:  rt.UnavailableReason(),
			UnavailableMessage: rt.UnavailableMessage(),
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

		// Derive ParamsSchema and paramsType.
		if pt := at.deriveParamsType(); pt != nil {
			at.paramsType = pt
			at.ParamsSchema = pt.ToJSONSchema()
		}

		tools = append(tools, at)
	}
	return AgentView{Tools: tools}
}

func cloneAgentView(view AgentView) AgentView {
	if view.Tools == nil {
		return AgentView{}
	}
	tools := make([]AgentTool, len(view.Tools))
	for i, tool := range view.Tools {
		tools[i] = cloneAgentTool(tool)
	}
	return AgentView{Tools: tools}
}

func cloneAgentTool(tool AgentTool) AgentTool {
	tool.ParamsSchema = cloneAnyMap(tool.ParamsSchema)
	tool.ResourceUses = tooldef.CloneResourceUses(tool.ResourceUses)
	tool.hiddenParams = cloneBoolMap(tool.hiddenParams)
	tool.boundLiterals = cloneAnyMap(tool.boundLiterals)
	return tool
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, elem := range typed {
			out[i] = cloneAny(elem)
		}
		return out
	default:
		return value
	}
}

func agentToolDescription(tool PreparedTool) string {
	description := strings.TrimSpace(tool.Description)
	if !tool.Unavailable() {
		return description
	}

	note := "Currently unavailable"
	if reason := strings.TrimSpace(tool.UnavailableMessage()); reason != "" {
		note += " because " + reason
	}
	note += "."

	if description == "" {
		return note
	}
	if strings.HasSuffix(description, ".") || strings.HasSuffix(description, "!") || strings.HasSuffix(description, "?") {
		return description + " " + note
	}
	return description + ". " + note
}
