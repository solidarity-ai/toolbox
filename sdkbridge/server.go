package sdkbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/solidarity-ai/toolbox/codemode"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

const (
	jsonRPCVersion      = "2.0"
	errCodeParse        = -32700
	errCodeInvalidReq   = -32600
	errCodeMethod       = -32601
	errCodeInvalidParam = -32602
	errCodeInternal     = -32603
)

var codeModeParamsSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"code": map[string]any{
			"type":        "string",
			"description": "TypeScript code to run against the composed toolset.",
		},
	},
	"required": []any{"code"},
}

type Options struct {
	Resolver               *registry.Resolver
	CredentialPolicySource toolset.PackageCredentialPolicySource
	Version                string
}

type Bridge struct {
	resolver               *registry.Resolver
	credentialPolicySource toolset.PackageCredentialPolicySource
	version                string

	mu          sync.RWMutex
	toolsets    map[string]*composedToolset
	nextToolset uint64
}

type composedToolset struct {
	mode     ComposeMode
	prepared toolset.PreparedToolset
	tools    []ToolDescriptor
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(opts Options) *Bridge {
	return &Bridge{
		resolver:               opts.Resolver,
		credentialPolicySource: opts.CredentialPolicySource,
		version:                resolveVersion(opts.Version),
		toolsets:               make(map[string]*composedToolset),
	}
}

func (b *Bridge) ServeStdio(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	reader := bufio.NewReader(stdin)

	for {
		if err := ctx.Err(); err != nil {
			break
		}

		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			// Preserve per-connection request order so lifecycle methods like
			// toolset.close and bridge.shutdown cannot overtake earlier invokes.
			resp := b.handle(ctx, append([]byte(nil), trimmed...))
			encoded, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return marshalErr
			}
			if _, writeErr := stdout.Write(append(encoded, '\n')); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return nil
}

func (b *Bridge) handle(ctx context.Context, payload []byte) rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return errorResponse(json.RawMessage("null"), errCodeParse, "parse error")
	}
	if req.JSONRPC != jsonRPCVersion {
		return errorResponse(normalizeID(req.ID), errCodeInvalidReq, "jsonrpc must be \"2.0\"")
	}
	if len(req.ID) == 0 || bytes.Equal(bytes.TrimSpace(req.ID), []byte("null")) {
		return errorResponse(json.RawMessage("null"), errCodeInvalidReq, "request id is required")
	}
	if strings.TrimSpace(req.Method) == "" {
		return errorResponse(req.ID, errCodeInvalidReq, "method is required")
	}

	result, err := b.handleMethod(ctx, req.Method, req.Params)
	if err != nil {
		var methodErr *methodError
		if errors.As(err, &methodErr) {
			return errorResponse(req.ID, methodErr.Code, methodErr.Message)
		}
		return errorResponse(req.ID, errCodeInternal, err.Error())
	}

	return rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      req.ID,
		Result:  result,
	}
}

func (b *Bridge) handleMethod(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "system.ping":
		return map[string]bool{"ok": true}, nil
	case "system.version":
		return SystemVersionResult{Version: b.version}, nil
	case "toolsetfile.load":
		var params ToolsetFileLoadParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.Path) == "" {
			return nil, invalidParams("path is required")
		}
		file, err := toolsetfile.Load(params.Path)
		if err != nil {
			return nil, err
		}
		return file, nil
	case "toolsetfile.write":
		var params ToolsetFileWriteParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.Path) == "" {
			return nil, invalidParams("path is required")
		}
		if len(params.Toolset) == 0 {
			return nil, invalidParams("toolset is required")
		}
		file, err := toolsetfile.Parse(params.Toolset)
		if err != nil {
			return nil, err
		}
		if err := file.Write(params.Path); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "toolset.compose":
		var params ComposeParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.compose(ctx, params)
	case "toolset.close":
		var params ToolsetCloseParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if strings.TrimSpace(params.ToolsetID) == "" {
			return nil, invalidParams("toolset_id is required")
		}
		b.closeToolset(params.ToolsetID)
		return map[string]any{}, nil
	case "tool.invoke":
		var params ToolInvokeParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.invoke(params)
	case "bridge.shutdown":
		b.clearToolsets()
		return map[string]any{}, nil
	default:
		return nil, &methodError{Code: errCodeMethod, Message: fmt.Sprintf("method %q not found", method)}
	}
}

func (b *Bridge) compose(ctx context.Context, params ComposeParams) (ComposeResult, error) {
	if params.Mode != ComposeModeDirect && params.Mode != ComposeModeCodemode {
		return ComposeResult{}, invalidParams("mode must be \"direct\" or \"codemode\"")
	}
	if strings.TrimSpace(params.ToolsetFile) != "" && len(params.Toolset) != 0 {
		return ComposeResult{}, invalidParams("provide exactly one of toolset_file or toolset")
	}
	if strings.TrimSpace(params.ToolsetFile) == "" && len(params.Toolset) == 0 {
		return ComposeResult{}, invalidParams("provide exactly one of toolset_file or toolset")
	}

	var (
		file *toolsetfile.ToolsetFile
		err  error
	)
	if strings.TrimSpace(params.ToolsetFile) != "" {
		file, err = toolsetfile.Load(params.ToolsetFile)
	} else {
		file, err = toolsetfile.Parse(params.Toolset)
	}
	if err != nil {
		return ComposeResult{}, err
	}

	cfg := toolset.Config{
		CredentialPolicySource: b.credentialPolicySource,
	}
	if params.Config != nil {
		cfg = params.Config.toToolsetConfig(b.credentialPolicySource)
	}

	prepared, err := file.Prepare(ctx, b.resolver, cfg)
	if err != nil {
		return ComposeResult{}, err
	}

	descriptors := b.describeTools(params.Mode, prepared)
	id := fmt.Sprintf("ts_%d", atomic.AddUint64(&b.nextToolset, 1))

	b.mu.Lock()
	b.toolsets[id] = &composedToolset{
		mode:     params.Mode,
		prepared: prepared,
		tools:    descriptors,
	}
	b.mu.Unlock()

	return ComposeResult{
		ToolsetID: id,
		Tools:     descriptors,
	}, nil
}

func (b *Bridge) describeTools(mode ComposeMode, prepared toolset.PreparedToolset) []ToolDescriptor {
	if mode == ComposeModeCodemode {
		return []ToolDescriptor{{
			Name:         CodeModeToolName,
			Description:  "Run TypeScript code against the composed toolset.",
			ParamsSchema: cloneMap(codeModeParamsSchema),
		}}
	}

	view := prepared.AgentView()
	out := make([]ToolDescriptor, 0, len(view.Tools))
	for _, tool := range view.Tools {
		out = append(out, ToolDescriptor{
			Name:         tool.Name,
			Description:  tool.Description,
			ParamsSchema: cloneMap(tool.ParamsSchema),
		})
	}
	return out
}

func (b *Bridge) invoke(params ToolInvokeParams) (ToolInvokeResult, error) {
	if strings.TrimSpace(params.ToolsetID) == "" {
		return ToolInvokeResult{}, invalidParams("toolset_id is required")
	}
	if strings.TrimSpace(params.ToolName) == "" {
		return ToolInvokeResult{}, invalidParams("tool_name is required")
	}

	handle, ok := b.lookupToolset(params.ToolsetID)
	if !ok {
		return ToolInvokeResult{}, invalidParams(fmt.Sprintf("unknown toolset_id %q", params.ToolsetID))
	}

	if handle.mode == ComposeModeCodemode {
		if params.ToolName != CodeModeToolName {
			return ToolInvokeResult{}, invalidParams(fmt.Sprintf("unknown tool %q", params.ToolName))
		}
		code, ok := params.Params["code"].(string)
		if !ok || strings.TrimSpace(code) == "" {
			return ToolInvokeResult{}, invalidParams("codemode tool requires params.code")
		}
		result, err := codemode.Run(handle.prepared, code)
		if err != nil {
			return ToolInvokeResult{}, err
		}
		return ToolInvokeResult{Content: result}, nil
	}

	result, err := invoke.Run(handle.prepared, params.ToolName, params.Params)
	if err != nil {
		return ToolInvokeResult{}, err
	}
	return ToolInvokeResult{Content: result}, nil
}

func (b *Bridge) lookupToolset(id string) (*composedToolset, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handle, ok := b.toolsets[id]
	return handle, ok
}

func (b *Bridge) closeToolset(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.toolsets, id)
}

func (b *Bridge) clearToolsets() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toolsets = make(map[string]*composedToolset)
}

func (c *ComposeConfig) toToolsetConfig(source toolset.PackageCredentialPolicySource) toolset.Config {
	cfg := toolset.Config{
		CredentialPolicySource: source,
	}
	if c == nil {
		return cfg
	}

	if len(c.Tools) > 0 {
		cfg.Tools = make([]toolset.BoundTool, 0, len(c.Tools))
		for _, tool := range c.Tools {
			bindings := make(map[string]toolset.Binding, len(tool.Bindings))
			for name, binding := range tool.Bindings {
				bindings[name] = toolset.Binding{
					Value:  binding.Value,
					Hidden: binding.Hidden,
					Check:  binding.Check,
				}
			}
			cfg.Tools = append(cfg.Tools, toolset.BoundTool{
				ToolRef:  tool.ToolRef,
				Bindings: bindings,
			})
		}
	}
	if len(c.ResourceBindings) > 0 {
		cfg.ResourceBindings = make(map[string]toolset.Binding, len(c.ResourceBindings))
		for name, binding := range c.ResourceBindings {
			cfg.ResourceBindings[name] = toolset.Binding{
				Value:  binding.Value,
				Hidden: binding.Hidden,
				Check:  binding.Check,
			}
		}
	}
	if len(c.EnvContext) > 0 {
		cfg.EnvContext = make(map[string]any, len(c.EnvContext))
		for key, value := range c.EnvContext {
			cfg.EnvContext[key] = value
		}
	}
	return cfg
}

type methodError struct {
	Code    int
	Message string
}

func (e *methodError) Error() string { return e.Message }

func invalidParams(msg string) error {
	return &methodError{Code: errCodeInvalidParam, Message: msg}
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return invalidParams(err.Error())
	}
	return nil
}

func errorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      normalizeID(id),
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
}

func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func resolveVersion(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	default:
		return v
	}
}
