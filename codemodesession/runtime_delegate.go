package codemodesession

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/dop251/goja"
	"github.com/microsoft/typescript-go/toolbox"
	repl "github.com/mackross/repljs"
	replengine "github.com/mackross/repljs/engine"
	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

type runtimeDelegate struct {
	prepared func() toolset.PreparedToolset
}

type runtimeBinding struct {
	prepared func() toolset.PreparedToolset
	state    runtimeToolState
}

type runtimeState struct {
	Tools []runtimeToolState `json:"tools,omitempty"`
}

type runtimeToolState struct {
	Name         string   `json:"name"`
	Package      string   `json:"package"`
	Params       []string `json:"params,omitempty"`
	ReplayPolicy string   `json:"replayPolicy"`
}

func newRuntimeDelegate(prepared func() toolset.PreparedToolset) repl.VMDelegate {
	if prepared == nil {
		prepared = func() toolset.PreparedToolset { return toolset.PreparedToolset{} }
	}
	return runtimeDelegate{prepared: prepared}
}

func (d runtimeDelegate) ConfigureRuntime(ctx repl.SessionRuntimeContext, rt *goja.Runtime, host repl.HostFuncBuilder, state json.RawMessage) (json.RawMessage, error) {
	bindings, nextState, err := configureRuntimeBindings(d.prepared, state)
	if err != nil {
		return nil, err
	}
	if err := installRuntimeBindings(rt, host, bindings, ctx.IsCurrentRuntime); err != nil {
		return nil, err
	}
	return nextState, nil
}

func (d runtimeDelegate) TransitionRuntime(ctx repl.RuntimeTransitionContext, rt *goja.Runtime, host repl.HostFuncBuilder, fromState, toState json.RawMessage) error {
	fromDecoded, err := decodeRuntimeState(fromState)
	if err != nil {
		return err
	}
	toDecoded, err := decodeRuntimeState(toState)
	if err != nil {
		return err
	}
	if err := removeRuntimeBindings(rt, fromDecoded.Tools); err != nil {
		return err
	}
	return installRuntimeBindings(rt, host, runtimeBindingsFromState(d.prepared, toDecoded.Tools), ctx.IsCurrentRuntime)
}

func configureRuntimeBindings(prepared func() toolset.PreparedToolset, state json.RawMessage) ([]runtimeBinding, json.RawMessage, error) {
	if len(state) == 0 {
		nextState, err := runtimeStateJSON(prepared())
		if err != nil {
			return nil, nil, err
		}
		decoded, err := decodeRuntimeState(nextState)
		if err != nil {
			return nil, nil, err
		}
		return runtimeBindingsFromState(prepared, decoded.Tools), nextState, nil
	}
	decoded, err := decodeRuntimeState(state)
	if err != nil {
		return nil, nil, err
	}
	normalized, err := normalizeRawJSON(state)
	if err != nil {
		return nil, nil, fmt.Errorf("decode persisted runtime state: %w", err)
	}
	return runtimeBindingsFromState(prepared, decoded.Tools), normalized, nil
}

func buildRuntimeToolStates(prepared toolset.PreparedToolset) []runtimeToolState {
	view := prepared.AgentView()
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	out := make([]runtimeToolState, 0, len(tools))
	for _, tool := range tools {
		preparedTool, ok := prepared.Tool(tool.Name)
		if !ok {
			continue
		}
		toolState := runtimeToolState{
			Name:         preparedTool.Name,
			Package:      preparedToolPackageName(preparedTool),
			ReplayPolicy: string(replayPolicyForTool(preparedTool.Effect, preparedTool.Idempotent)),
		}
		if tool.Sig != nil {
			hidden := tool.HiddenParams()
			for _, param := range tool.Sig.Params() {
				if hidden[param.Name()] {
					continue
				}
				toolState.Params = append(toolState.Params, param.Name())
			}
		}
		out = append(out, toolState)
	}
	return out
}

func runtimeBindingsFromState(prepared func() toolset.PreparedToolset, toolStates []runtimeToolState) []runtimeBinding {
	out := make([]runtimeBinding, 0, len(toolStates))
	for _, toolState := range toolStates {
		out = append(out, runtimeBinding{
			prepared: prepared,
			state: runtimeToolState{
				Name:         toolState.Name,
				Package:      toolState.Package,
				Params:       append([]string(nil), toolState.Params...),
				ReplayPolicy: toolState.ReplayPolicy,
			},
		})
	}
	return out
}

func marshalRuntimeState(toolStates []runtimeToolState) (json.RawMessage, error) {
	state := runtimeState{Tools: make([]runtimeToolState, 0, len(toolStates))}
	state.Tools = append(state.Tools, toolStates...)
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime state: %w", err)
	}
	return data, nil
}

func runtimeStateJSON(prepared toolset.PreparedToolset) (json.RawMessage, error) {
	return marshalRuntimeState(buildRuntimeToolStates(prepared))
}

func decodeRuntimeState(raw json.RawMessage) (runtimeState, error) {
	if len(raw) == 0 {
		return runtimeState{}, nil
	}
	normalized, err := normalizeRawJSON(raw)
	if err != nil {
		return runtimeState{}, fmt.Errorf("decode persisted runtime state: %w", err)
	}
	var decoded runtimeState
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return runtimeState{}, fmt.Errorf("decode persisted runtime state: %w", err)
	}
	return decoded, nil
}

func normalizeRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func installRuntimeBindings(rt *goja.Runtime, host repl.HostFuncBuilder, bindings []runtimeBinding, isCurrentRuntime func() bool) error {
	global := rt.GlobalObject()
	for _, binding := range bindings {
		pkgObj, err := ensureObjectPath(rt, global, packageNamespaceSegments(binding.state.Package))
		if err != nil {
			return fmt.Errorf("install package %q: %w", binding.state.Package, err)
		}

		toolSegments := splitDotted(binding.state.Name)
		if len(toolSegments) == 0 {
			continue
		}
		parent, err := ensureObjectPath(rt, pkgObj, sanitizeSegments(toolSegments[:len(toolSegments)-1]))
		if err != nil {
			return fmt.Errorf("install tool namespace for %q: %w", binding.state.Name, err)
		}
		wrapper, err := buildRuntimeWrapper(rt, host, binding, isCurrentRuntime)
		if err != nil {
			return fmt.Errorf("build tool %q wrapper: %w", binding.state.Name, err)
		}
		if err := parent.Set(sanitizeIdentifierSegment(toolSegments[len(toolSegments)-1]), wrapper); err != nil {
			return fmt.Errorf("install tool %q: %w", binding.state.Name, err)
		}
	}
	return nil
}

func removeRuntimeBindings(rt *goja.Runtime, toolStates []runtimeToolState) error {
	global := rt.GlobalObject()
	for _, toolState := range toolStates {
		if err := removeRuntimeBinding(global, toolState); err != nil {
			return err
		}
	}
	return nil
}

func removeRuntimeBinding(global *goja.Object, toolState runtimeToolState) error {
	path := append(packageNamespaceSegments(toolState.Package), sanitizeSegments(splitDotted(toolState.Name))...)
	if len(path) == 0 {
		return nil
	}
	objects := make([]*goja.Object, 1, len(path))
	objects[0] = global
	current := global
	for _, segment := range path[:len(path)-1] {
		next, ok := current.Get(segment).(*goja.Object)
		if !ok || next == nil {
			return nil
		}
		objects = append(objects, next)
		current = next
	}
	if err := current.Delete(path[len(path)-1]); err != nil {
		return err
	}
	for i := len(objects) - 1; i >= 1; i-- {
		obj := objects[i]
		if len(obj.Keys()) != 0 {
			break
		}
		if err := objects[i-1].Delete(path[i-1]); err != nil {
			return err
		}
	}
	return nil
}

func buildRuntimeWrapper(rt *goja.Runtime, host repl.HostFuncBuilder, binding runtimeBinding, isCurrentRuntime func() bool) (goja.Value, error) {
	staleMessage := fmt.Sprintf("tool %s came from a previous runtime and is no longer callable", binding.state.Name)
	rawInvoke := host.WrapSync(binding.state.Name, binding.replay(), func(ctx context.Context, params []byte) ([]byte, error) {
		args, err := decodeRuntimeArgs(params)
		if err != nil {
			return nil, fmt.Errorf("%s: decode args: %w", binding.state.Name, err)
		}
		prepared := binding.prepared()
		tool, ok := prepared.Tool(binding.state.Name)
		if !ok {
			return nil, fmt.Errorf("tool %s unavailable for live replay", binding.state.Name)
		}
		result, err := invoke.RunContext(ctx, prepared, binding.state.Name, args)
		if err != nil {
			return nil, err
		}
		return encodeRuntimeResult(currentReturnType(tool), result)
	})

	wrapper := func(call goja.FunctionCall) goja.Value {
		if isCurrentRuntime != nil && !isCurrentRuntime() {
			panic(rt.NewTypeError("%s", staleMessage))
		}
		argsObj := rt.NewObject()
		for i, paramName := range binding.state.Params {
			if i >= len(call.Arguments) {
				break
			}
			_ = argsObj.Set(paramName, call.Arguments[i])
		}
		return rawInvoke(goja.FunctionCall{
			This:      goja.Undefined(),
			Arguments: []goja.Value{argsObj},
		})
	}
	wrapped := rt.ToValue(wrapper)
	replengine.SetIndexedValueMetadata(wrapped, replengine.IndexedValueMetadata{StaleMessage: staleMessage})
	return wrapped, nil
}

func (b runtimeBinding) replay() repl.ReplayPolicy {
	switch b.state.ReplayPolicy {
	case string(repl.ReplayReadonly):
		return repl.ReplayReadonly
	case string(repl.ReplayIdempotent):
		return repl.ReplayIdempotent
	default:
		return repl.ReplayNonReplayable
	}
}

func currentReturnType(tool toolset.PreparedTool) *toolbox.TSType {
	if tool.Sig == nil || tool.Sig.Return() == nil {
		return nil
	}
	return tool.Sig.Return().UnwrapPromise()
}

func decodeRuntimeArgs(params []byte) (map[string]any, error) {
	value, err := jswire.DecodeGoja(goja.New(), params)
	if err != nil {
		return nil, err
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return map[string]any{}, nil
	}
	exported := value.Export()
	if exported == nil {
		return map[string]any{}, nil
	}
	args, ok := exported.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object args, got %T", exported)
	}
	return args, nil
}

func encodeRuntimeResult(returnType *toolbox.TSType, raw string) ([]byte, error) {
	return jswire.EncodeGoja(goja.New().ToValue(decodeRuntimeResult(returnType, raw)))
}

func decodeRuntimeResult(returnType *toolbox.TSType, raw string) any {
	trimmed := strings.TrimSpace(raw)
	if returnType != nil && strings.TrimSpace(returnType.ToTS()) == "string" {
		return raw
	}
	if decoded, ok := decodeJSONLiteral(trimmed); ok {
		return decoded
	}
	if returnType == nil {
		return raw
	}
	switch strings.TrimSpace(returnType.ToTS()) {
	case "void", "undefined", "null":
		return nil
	default:
		return raw
	}
}

func decodeJSONLiteral(raw string) (any, bool) {
	if !looksLikeJSONLiteral(raw) {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func looksLikeJSONLiteral(raw string) bool {
	if raw == "" {
		return false
	}
	switch raw[0] {
	case '{', '[', '"':
		return true
	case 't', 'f', 'n':
		return raw == "true" || raw == "false" || raw == "null"
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		_, err := strconv.ParseFloat(raw, 64)
		return err == nil
	default:
		return false
	}
}

func replayPolicyForTool(effect tooldef.Effect, idempotent *bool) repl.ReplayPolicy {
	if effect == tooldef.EffectReadOnly {
		return repl.ReplayReadonly
	}
	if idempotent != nil && *idempotent {
		return repl.ReplayIdempotent
	}
	return repl.ReplayNonReplayable
}

func ensureObjectPath(rt *goja.Runtime, root *goja.Object, segments []string) (*goja.Object, error) {
	current := root
	for _, segment := range segments {
		value := current.Get(segment)
		if obj, ok := value.(*goja.Object); ok && obj != nil {
			current = obj
			continue
		}
		next := rt.NewObject()
		if err := current.Set(segment, next); err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func packageNamespaceSegments(packageName string) []string {
	parts := splitDotted(packageName)
	if len(parts) == 0 {
		return []string{"pkg"}
	}
	return sanitizeSegments(parts)
}

func sanitizeSegments(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, sanitizeIdentifierSegment(part))
	}
	if len(out) == 0 {
		return []string{"pkg"}
	}
	return out
}

func splitDotted(value string) []string {
	raw := strings.Split(strings.TrimSpace(value), ".")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func sanitizeIdentifierSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "pkg"
	}
	var out []rune
	upperNext := false
	for _, r := range segment {
		switch {
		case r == '-' || r == '.' || r == ' ' || r == '/':
			upperNext = true
		case len(out) == 0 && (unicode.IsLetter(r) || r == '_' || r == '$'):
			out = append(out, r)
			upperNext = false
		case len(out) == 0 && unicode.IsDigit(r):
			out = append(out, '_', r)
			upperNext = false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$':
			if upperNext && unicode.IsLetter(r) {
				out = append(out, unicode.ToUpper(r))
			} else {
				out = append(out, r)
			}
			upperNext = false
		default:
			if !upperNext {
				out = append(out, '_')
			}
			upperNext = false
		}
	}
	if len(out) == 0 {
		return "pkg"
	}
	return string(out)
}
