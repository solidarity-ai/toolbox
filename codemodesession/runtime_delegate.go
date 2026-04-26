package codemodesession

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dop251/goja"
	repl "github.com/mackross/repljs"
	replengine "github.com/mackross/repljs/engine"
	"github.com/mackross/repljs/jswire"
	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

type runtimeDelegate struct {
	prepared  func() toolset.PreparedToolset
	toolCalls toolCallJournal
	approvals approvalStore
	executor  *invoke.Executor
	toolCtx   func(context.Context) (context.Context, func())
}

type runtimeBinding struct {
	prepared  func() toolset.PreparedToolset
	toolCalls toolCallJournal
	approvals approvalStore
	executor  *invoke.Executor
	toolCtx   func(context.Context) (context.Context, func())
	state     runtimeToolState
}

type runtimeState struct {
	Tools []runtimeToolState `json:"tools,omitempty"`
}

type runtimeToolState struct {
	Name          string   `json:"name"`
	Package       string   `json:"package"`
	Params        []string `json:"params,omitempty"`
	ReplayPolicy  string   `json:"replayPolicy"`
	NeedsApproval bool     `json:"needsApproval,omitempty"`
}

func newRuntimeDelegate(prepared func() toolset.PreparedToolset, toolCalls toolCallJournal, approvals approvalStore, executor *invoke.Executor, toolCtx func(context.Context) (context.Context, func())) repl.VMDelegate {
	if prepared == nil {
		prepared = func() toolset.PreparedToolset { return toolset.PreparedToolset{} }
	}
	return runtimeDelegate{prepared: prepared, toolCalls: toolCalls, approvals: approvals, executor: executor, toolCtx: toolCtx}
}

func (d runtimeDelegate) ConfigureRuntime(ctx repl.SessionRuntimeContext, rt *goja.Runtime, host repl.HostFuncBuilder, state json.RawMessage) (json.RawMessage, error) {
	bindings, nextState, err := configureRuntimeBindings(d.prepared, d.executor, d.toolCtx, state)
	if err != nil {
		return nil, err
	}
	if err := installRuntimeBindings(rt, host, bindings, d.toolCalls, d.approvals, ctx.SessionID, ctx.IsCurrentRuntime); err != nil {
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
	return installRuntimeBindings(rt, host, runtimeBindingsFromState(d.prepared, d.executor, d.toolCtx, toDecoded.Tools), d.toolCalls, d.approvals, ctx.SessionID, ctx.IsCurrentRuntime)
}

func configureRuntimeBindings(prepared func() toolset.PreparedToolset, executor *invoke.Executor, toolCtx func(context.Context) (context.Context, func()), state json.RawMessage) ([]runtimeBinding, json.RawMessage, error) {
	if len(state) == 0 {
		nextState, err := runtimeStateJSON(prepared())
		if err != nil {
			return nil, nil, err
		}
		decoded, err := decodeRuntimeState(nextState)
		if err != nil {
			return nil, nil, err
		}
		return runtimeBindingsFromState(prepared, executor, toolCtx, decoded.Tools), nextState, nil
	}
	decoded, err := decodeRuntimeState(state)
	if err != nil {
		return nil, nil, err
	}
	normalized, err := normalizeRawJSON(state)
	if err != nil {
		return nil, nil, fmt.Errorf("decode persisted runtime state: %w", err)
	}
	return runtimeBindingsFromState(prepared, executor, toolCtx, decoded.Tools), normalized, nil
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
			Name:          preparedTool.Name,
			Package:       preparedToolPackageName(preparedTool),
			ReplayPolicy:  string(replayPolicyForTool(preparedTool.Effect, preparedTool.Idempotent)),
			NeedsApproval: preparedTool.NeedsApproval,
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

func runtimeBindingsFromState(prepared func() toolset.PreparedToolset, executor *invoke.Executor, toolCtx func(context.Context) (context.Context, func()), toolStates []runtimeToolState) []runtimeBinding {
	out := make([]runtimeBinding, 0, len(toolStates))
	for _, toolState := range toolStates {
		out = append(out, runtimeBinding{
			prepared:  prepared,
			toolCalls: nil,
			approvals: nil,
			executor:  executor,
			toolCtx:   toolCtx,
			state: runtimeToolState{
				Name:          toolState.Name,
				Package:       toolState.Package,
				Params:        append([]string(nil), toolState.Params...),
				ReplayPolicy:  toolState.ReplayPolicy,
				NeedsApproval: toolState.NeedsApproval,
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

func installRuntimeBindings(rt *goja.Runtime, host repl.HostFuncBuilder, bindings []runtimeBinding, toolCalls toolCallJournal, approvals approvalStore, sessionID repl.SessionID, isCurrentRuntime func() bool) error {
	global := rt.GlobalObject()
	if err := installToolCallInspector(rt, host, toolCalls, sessionID); err != nil {
		return err
	}
	for _, binding := range bindings {
		binding.toolCalls = toolCalls
		binding.approvals = approvals
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
		wrapper, err := buildRuntimeWrapper(rt, host, binding, sessionID, isCurrentRuntime)
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

func buildRuntimeWrapper(rt *goja.Runtime, host repl.HostFuncBuilder, binding runtimeBinding, sessionID repl.SessionID, isCurrentRuntime func() bool) (goja.Value, error) {
	staleMessage := fmt.Sprintf("tool %s came from a previous runtime and is no longer callable", binding.state.Name)
	rawApproval := host.WrapSyncWithEffectID(pendingApprovalEffectName(binding.state.Name), repl.ReplayReadonly, func(_ context.Context, effectID repl.EffectID, params []byte) ([]byte, error) {
		toolCallID := string(effectID)
		reviewedToolKey := approvalReviewedToolKey(binding.prepared(), binding.state.Name)
		if binding.toolCalls != nil {
			if err := binding.toolCalls.EnsureNeedsApproval(sessionID, toolCallID, toolCallID, binding.state.Name, params); err != nil {
				return nil, err
			}
		}
		if binding.approvals != nil {
			call := approvalCallState{
				ToolCallID:      toolCallID,
				EffectID:        toolCallID,
				ToolName:        binding.state.Name,
				ReviewedToolKey: reviewedToolKey,
				Params:          params,
			}
			enrichApprovalCallState(&call, binding.prepared(), binding.state.Name)
			if err := binding.approvals.RecordPendingToolCall(sessionID, call); err != nil {
				return nil, err
			}
		}
		return encodeToolCallTaskValue(toolCallID)
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
		paramsEncoded, err := jswire.EncodeGoja(argsObj)
		if err != nil {
			panic(rt.NewTypeError("tool %s args: %v", binding.state.Name, err))
		}

		if binding.state.NeedsApproval {
			taskValue, effectID := rawApproval(goja.FunctionCall{
				This:      goja.Undefined(),
				Arguments: []goja.Value{argsObj},
			})
			if strings.TrimSpace(string(effectID)) == "" {
				panic(rt.NewTypeError("tool %s toolCallId: missing effect id", binding.state.Name))
			}
			taskObj := taskValue.ToObject(rt)
			if taskObj == nil {
				panic(rt.NewTypeError("tool %s returned a non-task value", binding.state.Name))
			}
			return taskObj
		}

		startGate := newToolCallStartGate()
		rawInvoke := host.WrapAsyncWithEffectID(binding.state.Name, binding.replay(), func(ctx context.Context, _ repl.EffectID, params []byte) ([]byte, error) {
			if err := startGate.Wait(ctx); err != nil {
				return nil, err
			}
			args, err := decodeRuntimeArgs(params)
			if err != nil {
				return nil, fmt.Errorf("%s: decode args: %w", binding.state.Name, err)
			}
			prepared := binding.prepared()
			tool, ok := prepared.Tool(binding.state.Name)
			if !ok {
				return nil, fmt.Errorf("tool %s unavailable for live replay", binding.state.Name)
			}
			execCtx := ctx
			release := func() {}
			if binding.toolCtx != nil {
				execCtx, release = binding.toolCtx(ctx)
			}
			defer release()
			result, err := runPreparedTool(execCtx, binding.executor, prepared, binding.state.Name, args)
			if err != nil {
				return nil, err
			}
			return encodeRuntimeResult(currentReturnType(tool), result)
		})

		promiseValue, effectID := rawInvoke(goja.FunctionCall{
			This:      goja.Undefined(),
			Arguments: []goja.Value{argsObj},
		})
		toolCallID := strings.TrimSpace(string(effectID))
		if toolCallID == "" {
			startGate.Fail(fmt.Errorf("tool %s toolCallId: missing effect id", binding.state.Name))
			panic(rt.NewTypeError("tool %s toolCallId: missing effect id", binding.state.Name))
		}
		if binding.toolCalls != nil {
			if err := binding.toolCalls.EnsureStarted(sessionID, toolCallID, binding.state.Name, paramsEncoded); err != nil {
				startGate.Fail(fmt.Errorf("tool %s start journal: %v", binding.state.Name, err))
				panic(rt.NewTypeError("tool %s start journal: %v", binding.state.Name, err))
			}
		}
		task := newToolCallTaskValue(rt, toolCallID)

		promiseObj := promiseValue.ToObject(rt)
		if promiseObj == nil {
			startGate.Fail(fmt.Errorf("tool %s returned a non-promise value", binding.state.Name))
			panic(rt.NewTypeError("tool %s returned a non-promise value", binding.state.Name))
		}
		if err := promiseObj.Set("toolCallTask", task); err != nil {
			startGate.Fail(fmt.Errorf("tool %s attach task: %v", binding.state.Name, err))
			panic(rt.NewTypeError("tool %s attach task: %v", binding.state.Name, err))
		}
		if err := attachToolCallSettlers(rt, promiseObj, binding.toolCalls, sessionID, toolCallID); err != nil {
			startGate.Fail(fmt.Errorf("tool %s attach settlers: %v", binding.state.Name, err))
			panic(rt.NewTypeError("tool %s attach settlers: %v", binding.state.Name, err))
		}
		startGate.Allow()
		return promiseObj
	}
	wrapped := rt.ToValue(wrapper)
	replengine.SetIndexedValueMetadata(wrapped, replengine.IndexedValueMetadata{StaleMessage: staleMessage})
	return wrapped, nil
}

func enrichApprovalCallState(call *approvalCallState, prepared toolset.PreparedToolset, toolName string) {
	if call == nil {
		return
	}
	tool, ok := prepared.Tool(toolName)
	if !ok {
		call.FullToolName = toolName
		call.ToolLabel = toolName
		return
	}
	pkg := preparedToolPackageName(tool)
	call.PackageKey = pkg
	call.PackageLabel = pkg
	call.ToolLabel = tool.Name
	call.FullToolName = tool.ToolApprovalKey()
	call.Description = strings.TrimSpace(tool.Description)
	call.Presentation = approvalPresentationForTool(tool, call.Params)
}

func approvalPresentationForTool(tool toolset.PreparedTool, params []byte) json.RawMessage {
	args, err := decodeRuntimeArgs(params)
	if err != nil {
		return nil
	}
	args = approvalPresentationArgsForTool(tool, args)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var raw string
	switch {
	case tool.TS != nil:
		raw, err = quickts.RunApprovalPresentation(ctx, *tool.TS, args, nil, tool.Sig)
	case tool.TSWasm != nil:
		raw, err = quickts.RunApprovalPresentation(ctx, tool.TSWasm.TSToolDef, args, nil, tool.Sig)
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return nil
	}
	if !validApprovalPresentation([]byte(trimmed)) {
		return nil
	}
	return append(json.RawMessage(nil), trimmed...)
}

func approvalPresentationArgsForTool(tool toolset.PreparedTool, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+len(tool.AccountParams()))
	for k, v := range args {
		out[k] = v
	}
	fullParams, err := tool.ValidateCall(args)
	if err != nil {
		fullParams = args
	}
	for paramName, account := range tool.SelectedCredentialAccountParams(fullParams) {
		if current, ok := out[paramName]; ok {
			if currentString, ok := current.(string); !ok || strings.TrimSpace(currentString) != "" {
				continue
			}
		}
		out[paramName] = account
	}
	return out
}

func validApprovalPresentation(raw []byte) bool {
	var value struct {
		Schema string            `json:"schema"`
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	if value.Schema != "toolbox.approval.presentation.v1" {
		return false
	}
	for _, blockRaw := range value.Blocks {
		var block struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(blockRaw, &block); err != nil {
			return false
		}
		switch block.Type {
		case "fields", "text", "list":
		default:
			return false
		}
	}
	return true
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

func installToolCallInspector(rt *goja.Runtime, host repl.HostFuncBuilder, toolCalls toolCallJournal, sessionID repl.SessionID) error {
	if rt == nil {
		return nil
	}
	inspectByID := host.WrapSync("__tool_call_by_id", repl.ReplayReadonly, func(ctx context.Context, params []byte) ([]byte, error) {
		toolCallID, err := decodeToolCallIDArg(params)
		if err != nil {
			return nil, err
		}
		if toolCalls == nil {
			return nil, fmt.Errorf("tool call inspection is unavailable")
		}
		snapshot, ok, err := toolCalls.Snapshot(sessionID, toolCallID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("unknown tool call %q", toolCallID)
		}
		value, err := toolCallSnapshotValue(snapshot)
		if err != nil {
			return nil, err
		}
		return jswire.EncodeGoja(goja.New().ToValue(value))
	})

	return rt.Set("$tool_call", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(rt.NewTypeError("$tool_call requires a ToolCallPromise, ToolCallTask, or toolCallId string"))
		}
		toolCallID, err := extractToolCallIDFromRef(rt, call.Arguments[0])
		if err != nil {
			panic(rt.NewTypeError("%s", err.Error()))
		}
		return inspectByID(goja.FunctionCall{
			This:      goja.Undefined(),
			Arguments: []goja.Value{rt.ToValue(toolCallID)},
		})
	})
}

func currentReturnType(tool toolset.PreparedTool) *toolbox.TSType {
	if tool.Sig == nil || tool.Sig.Return() == nil {
		return nil
	}
	return tool.Sig.Return().UnwrapPromise()
}

func newToolCallTaskValue(rt *goja.Runtime, toolCallID string) *goja.Object {
	task := rt.NewObject()
	_ = task.Set("toolCallId", toolCallID)
	return task
}

func encodeToolCallTaskValue(toolCallID string) ([]byte, error) {
	return jswire.EncodeGoja(goja.New().ToValue(map[string]any{
		"toolCallId": toolCallID,
	}))
}

func pendingApprovalEffectName(toolName string) string {
	return "__pending_approval." + strings.TrimSpace(toolName)
}

func attachToolCallSettlers(rt *goja.Runtime, promise *goja.Object, toolCalls toolCallJournal, sessionID repl.SessionID, toolCallID string) error {
	if rt == nil || promise == nil || toolCalls == nil {
		return nil
	}
	thenValue := promise.Get("then")
	thenFn, ok := goja.AssertFunction(thenValue)
	if !ok {
		return fmt.Errorf("promise.then is not callable")
	}

	onFulfilled := rt.ToValue(func(call goja.FunctionCall) goja.Value {
		var result []byte
		if len(call.Arguments) > 0 {
			encoded, err := jswire.EncodeGoja(call.Arguments[0])
			if err == nil {
				result = encoded
			}
		}
		_ = toolCalls.EnsureCompleted(sessionID, toolCallID, result)
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		return call.Arguments[0]
	})
	onRejected := rt.ToValue(func(call goja.FunctionCall) goja.Value {
		errText := "tool call failed"
		if len(call.Arguments) > 0 {
			errText = toolCallErrorString(call.Arguments[0])
		}
		if isToolCallContextCancellation(errText) {
			_ = toolCalls.EnsureCancelled(sessionID, toolCallID)
		} else {
			_ = toolCalls.EnsureFailed(sessionID, toolCallID, errText)
		}
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		return call.Arguments[0]
	})

	_, err := thenFn(promise, onFulfilled, onRejected)
	return err
}

func decodeToolCallIDArg(params []byte) (string, error) {
	value, err := jswire.DecodeGoja(goja.New(), params)
	if err != nil {
		return "", fmt.Errorf("decode tool call ref: %w", err)
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", fmt.Errorf("tool call ref must not be empty")
	}
	return strings.TrimSpace(value.String()), nil
}

func extractToolCallIDFromRef(rt *goja.Runtime, value goja.Value) (string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", fmt.Errorf("$tool_call expects a ToolCallPromise, ToolCallTask, or toolCallId string")
	}
	if text, ok := value.Export().(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("tool call ref must not be empty")
		}
		return text, nil
	}
	obj, ok := value.(*goja.Object)
	if !ok || obj == nil {
		return "", fmt.Errorf("$tool_call expects a ToolCallPromise, ToolCallTask, or toolCallId string")
	}
	if taskValue := obj.Get("toolCallTask"); taskValue != nil && taskValue != goja.Undefined() && taskValue != goja.Null() {
		taskObj := taskValue.ToObject(rt)
		if taskObj != nil {
			if toolCallID := strings.TrimSpace(taskObj.Get("toolCallId").String()); toolCallID != "" && toolCallID != "undefined" {
				return toolCallID, nil
			}
		}
	}
	if toolCallID := strings.TrimSpace(obj.Get("toolCallId").String()); toolCallID != "" && toolCallID != "undefined" {
		return toolCallID, nil
	}
	return "", fmt.Errorf("$tool_call expects a ToolCallPromise, ToolCallTask, or toolCallId string")
}

func toolCallErrorString(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "tool call failed"
	}
	if obj, ok := value.(*goja.Object); ok {
		stack := obj.Get("stack")
		if stack != nil && stack != goja.Undefined() && stack != goja.Null() {
			if text := strings.TrimSpace(stack.String()); text != "" {
				return text
			}
		}
	}
	if text := strings.TrimSpace(value.String()); text != "" {
		return text
	}
	return "tool call failed"
}

type toolCallStartGate struct {
	once   sync.Once
	result chan error
}

func newToolCallStartGate() *toolCallStartGate {
	return &toolCallStartGate{result: make(chan error, 1)}
}

func (g *toolCallStartGate) Allow() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		g.result <- nil
	})
}

func (g *toolCallStartGate) Fail(err error) {
	if g == nil {
		return
	}
	g.once.Do(func() {
		g.result <- err
	})
}

func (g *toolCallStartGate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	select {
	case err := <-g.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isToolCallContextCancellation(errText string) bool {
	errText = strings.ToLower(strings.TrimSpace(errText))
	return strings.Contains(errText, "context canceled") ||
		strings.Contains(errText, "context cancelled") ||
		strings.Contains(errText, "context deadline exceeded")
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

func approvalReviewedToolKey(prepared toolset.PreparedToolset, toolName string) string {
	tool, ok := prepared.Tool(toolName)
	if !ok {
		return ""
	}
	return tool.ApprovalFingerprint()
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
	parts := sanitizeSegments(splitDotted(packageName))
	if len(parts) == 0 {
		return []string{"pkg"}
	}
	return parts
}

func sanitizeSegments(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, sanitizeIdentifierSegment(part))
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
