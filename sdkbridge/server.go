package sdkbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/internal/buildinfo"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/toolpkgdiscovery"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
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

func codeModeParamsSchema(locked bool) map[string]any {
	properties := map[string]any{
		codemodesession.TypeScriptCellSourceParam: map[string]any{
			"type":        "string",
			"description": "TypeScript code (can be multiline) for next cell.",
		},
		codemodesession.TimeoutSecsParam: map[string]any{
			"type":        "number",
			"description": fmt.Sprintf("Optional. Maximum seconds to allow this cell to run before it fails. Use a larger value for long-running network or tool-heavy work. Defaults to %g.", codemodesession.DefaultSubmitTimeout.Seconds()),
			"default":     codemodesession.DefaultSubmitTimeout.Seconds(),
			"minimum":     0.001,
		},
	}
	required := []any{codemodesession.TypeScriptCellSourceParam}
	if !locked {
		properties[codemodesession.TBSessionParam] = map[string]any{
			"type":        "string",
			"description": "Notebook identity. Reuse the same tb_session to continue the same notebook.",
		}
		required = append(required, codemodesession.TBSessionParam)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func codeModeAwaitParamsSchema(locked bool) map[string]any {
	properties := map[string]any{}
	var required []any
	if !locked {
		properties[codemodesession.TBSessionParam] = map[string]any{
			"type":        "string",
			"description": "Notebook identity. Reuse the same tb_session to continue the same notebook.",
		}
		required = []any{codemodesession.TBSessionParam}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

type Options struct {
	Resolver               *registry.Resolver
	CredentialPolicySource toolset.PackageCredentialPolicySource
	CredentialRepository   *credentialrepo.Repository
	SearchClientFactory    func() (toolsetctl.SearchClient, error)
	PreparedToolsConsumer  toolsetctl.PreparedToolConsumer
	Version                string
}

type Bridge struct {
	resolver               *registry.Resolver
	credentialPolicySource toolset.PackageCredentialPolicySource
	credentialRepository   *credentialrepo.Repository
	searchClientFactory    func() (toolsetctl.SearchClient, error)
	preparedToolsConsumer  toolsetctl.PreparedToolConsumer
	version                string

	mu          sync.RWMutex
	toolsets    map[string]*composedToolset
	nextToolset uint64
}

type reloadableToolsetBackend interface {
	toolsetctl.ToolsetBackend
	Reload(context.Context) (toolset.PreparedToolset, error)
}

type composedToolset struct {
	mu       sync.RWMutex
	owner    *Bridge
	mode     ComposeMode
	prepared toolset.PreparedToolset
	tools    []ToolDescriptor
	manager  *codemodesession.Manager
	backend  toolsetctl.ToolsetBackend
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
		credentialRepository:   opts.CredentialRepository,
		searchClientFactory:    opts.SearchClientFactory,
		preparedToolsConsumer:  opts.PreparedToolsConsumer,
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
	case "codemode.session.new":
		return b.newCodemodeSession(ctx)
	case "toolset.search":
		var params ToolsetSearchParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.search(ctx, params)
	case "toolset.inspect":
		var params ToolsetInspectParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.inspect(ctx, params)
	case "toolset.install":
		var params ToolsetInstallParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.install(ctx, params)
	case "toolset.uninstall":
		var params ToolsetUninstallParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.uninstall(ctx, params)
	case "toolset.auth":
		var params ToolsetAuthParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return b.auth(ctx, params)
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
		return b.invoke(ctx, params)
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
	if b.resolver != nil {
		cfg.PackageGuard = b.resolver.PackageGuard()
	}

	var manager *codemodesession.Manager
	if params.Mode == ComposeModeCodemode {
		currentDir := composeCurrentDir(params.ToolsetFile)
		if strings.TrimSpace(params.TBSession) != "" {
			manager, err = codemodesession.OpenLockedManager(ctx, params.TBSession, currentDir, codemodesession.SessionConfig{})
			if err != nil {
				return ComposeResult{}, err
			}
		} else {
			manager = codemodesession.NewUnlockedManager(currentDir, codemodesession.SessionConfig{})
		}
	}

	handle := &composedToolset{
		owner:   b,
		mode:    params.Mode,
		manager: manager,
	}
	if strings.TrimSpace(params.ToolsetFile) != "" {
		backend, err := toolsetctl.NewFileBackend(ctx, toolsetctl.FileBackendOptions{
			ToolsetPath:          params.ToolsetFile,
			Resolver:             b.resolver,
			Config:               cfg,
			SearchClientFactory:  b.searchClientFactory,
			CredentialRepository: b.credentialRepository,
			Consumer:             handle,
		})
		if err != nil {
			handle.close()
			return ComposeResult{}, err
		}
		handle.setBackend(backend)
	} else {
		if file.AgentAllowsToolsetManagement() {
			handle.close()
			return ComposeResult{}, fmt.Errorf("inline toolset compose does not support agent.unsafe.allow_toolset_management")
		}
		backend, err := newInlineBackend(ctx, inlineBackendOptions{
			File:                file,
			Resolver:            b.resolver,
			Config:              cfg,
			SearchClientFactory: b.searchClientFactory,
			Consumer:            handle,
		})
		if err != nil {
			handle.close()
			return ComposeResult{}, err
		}
		handle.setBackend(backend)
	}

	descriptors := handle.toolDescriptors()
	id := fmt.Sprintf("ts_%d", atomic.AddUint64(&b.nextToolset, 1))

	b.mu.Lock()
	b.toolsets[id] = handle
	b.mu.Unlock()
	b.publishPreparedTools()

	return ComposeResult{
		ToolsetID: id,
		Tools:     descriptors,
	}, nil
}

func (b *Bridge) newCodemodeSession(ctx context.Context) (CodemodeSessionNewResult, error) {
	session, err := codemodesession.CreateFresh(ctx, composeCurrentDir(""), codemodesession.SessionConfig{})
	if err != nil {
		return CodemodeSessionNewResult{}, err
	}
	defer session.Close()
	return CodemodeSessionNewResult{TBSession: session.TBSession()}, nil
}

func describeTools(mode ComposeMode, prepared toolset.PreparedToolset, manager *codemodesession.Manager) []ToolDescriptor {
	if mode == ComposeModeCodemode {
		metaSession := &codemodesession.Session{}
		metaSession.SetPreparedTools(prepared)
		locked := manager != nil && manager.Locked()
		awaitAvailable := prepared.HasApprovalTools()
		description := metaSession.SuperToolDescriptionForSurface(codemodesession.ToolSurfaceModeLocked, awaitAvailable)
		if !locked {
			description = metaSession.SuperToolDescriptionForSurface(codemodesession.ToolSurfaceModeUnlocked, awaitAvailable)
		}
		tools := []ToolDescriptor{{
			Name:         CodeModeToolName,
			Description:  description,
			ParamsSchema: codeModeParamsSchema(locked),
		}}
		if !locked {
			tools = append([]ToolDescriptor{{
				Name:        CodeModeNewSessionToolName,
				Description: codemodesession.NewSessionToolDescription(awaitAvailable),
				ParamsSchema: map[string]any{
					"type":     "object",
					"required": []string{codemodesession.IntentParam},
					"properties": map[string]any{
						codemodesession.IntentParam: map[string]any{
							"type":        "string",
							"description": "User-facing task intent for this notebook. This appears as the approval-console context.",
						},
					},
					"additionalProperties": false,
				},
			}}, tools...)
		}
		if prepared.HasApprovalTools() {
			tools = append(tools, ToolDescriptor{
				Name:         CodeModeAwaitApprovalsToolName,
				Description:  "Wait for the outstanding approval(s) to be handled. Returns immediately with (no outstanding approvals). when nothing is waiting. Get as many approvals done as possible before calling, then call again if more are still waiting.",
				ParamsSchema: codeModeAwaitParamsSchema(locked),
			})
		}
		return tools
	}

	view := prepared.AgentView()
	out := make([]ToolDescriptor, 0, len(view.Tools))
	for _, tool := range view.Tools {
		preparedTool, ok := prepared.Tool(tool.Name)
		if !ok || !preparedTool.JSONCallable() {
			continue
		}
		out = append(out, ToolDescriptor{
			Name:         tool.Name,
			Description:  tool.Description,
			ParamsSchema: cloneMap(tool.ParamsSchema),
		})
	}
	return out
}

func (h *composedToolset) SetPreparedTools(prepared toolset.PreparedToolset) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.prepared = prepared
	if h.manager != nil {
		h.manager.SetPreparedTools(prepared)
	}
	h.tools = describeTools(h.mode, prepared, h.manager)
	owner := h.owner
	h.mu.Unlock()
	if owner != nil {
		owner.publishPreparedTools()
	}
}

func (h *composedToolset) setBackend(backend toolsetctl.ToolsetBackend) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.backend = backend
}

func (h *composedToolset) backendSnapshot() toolsetctl.ToolsetBackend {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.backend
}

func (h *composedToolset) snapshot() (ComposeMode, toolset.PreparedToolset, *codemodesession.Manager) {
	if h == nil {
		return ComposeModeDirect, toolset.PreparedToolset{}, nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.mode, h.prepared, h.manager
}

func (h *composedToolset) toolDescriptors() []ToolDescriptor {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneToolDescriptors(h.tools)
}

func (h *composedToolset) close() {
	if h == nil {
		return
	}
	h.mu.RLock()
	manager := h.manager
	h.mu.RUnlock()
	if manager != nil {
		_ = manager.Close()
	}
}

func (b *Bridge) invoke(ctx context.Context, params ToolInvokeParams) (ToolInvokeResult, error) {
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

	mode, prepared, manager := handle.snapshot()
	if mode == ComposeModeCodemode {
		if manager == nil {
			return ToolInvokeResult{}, fmt.Errorf("codemode session manager is not available")
		}
		switch params.ToolName {
		case CodeModeToolName:
			code, ok := params.Params[codemodesession.TypeScriptCellSourceParam].(string)
			if !ok || strings.TrimSpace(code) == "" {
				return ToolInvokeResult{}, invalidParams("codemode tool requires params." + codemodesession.TypeScriptCellSourceParam)
			}
			timeout, err := codeModeTimeout(params.Params)
			if err != nil {
				return ToolInvokeResult{}, invalidParams(err.Error())
			}
			tbSession, err := codeModeTBSession(manager, params.Params)
			if err != nil {
				return ToolInvokeResult{}, err
			}
			if ctx == nil {
				ctx = context.Background()
			}
			submitCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			content, err := manager.Submit(submitCtx, tbSession, code)
			if err != nil {
				return ToolInvokeResult{}, err
			}
			return ToolInvokeResult{Content: content}, nil
		case CodeModeAwaitApprovalsToolName:
			tbSession, err := codeModeTBSession(manager, params.Params)
			if err != nil {
				return ToolInvokeResult{}, err
			}
			result, err := manager.AwaitNextApproval(ctx, tbSession)
			if err != nil {
				return ToolInvokeResult{}, err
			}
			return ToolInvokeResult{Content: result.Text()}, nil
		case CodeModeNewSessionToolName:
			if manager.Locked() {
				return ToolInvokeResult{}, invalidParams(fmt.Sprintf("unknown tool %q", params.ToolName))
			}
			intent, _ := params.Params[codemodesession.IntentParam].(string)
			if strings.TrimSpace(intent) == "" {
				return ToolInvokeResult{}, invalidParams("new_super_tool_session requires params." + codemodesession.IntentParam)
			}
			tbSession, err := manager.CreateFreshSession(ctx, intent)
			if err != nil {
				return ToolInvokeResult{}, err
			}
			return ToolInvokeResult{Content: manager.NewSessionResult(tbSession)}, nil
		default:
			return ToolInvokeResult{}, invalidParams(fmt.Sprintf("unknown tool %q", params.ToolName))
		}
	}

	if tool, ok := prepared.Tool(params.ToolName); !ok || !tool.JSONCallable() {
		return ToolInvokeResult{}, invalidParams(fmt.Sprintf("unknown tool %q", params.ToolName))
	}
	args, err := invoke.EncodeInvokeArgs(params.Params)
	if err != nil {
		return ToolInvokeResult{}, err
	}
	result, err := invoke.RunContext(ctx, prepared, params.ToolName, args)
	if err != nil {
		return ToolInvokeResult{}, err
	}
	content, err := invoke.DecodeWireString(result)
	if err != nil {
		return ToolInvokeResult{}, err
	}
	return ToolInvokeResult{Content: content}, nil
}

func (b *Bridge) search(ctx context.Context, params ToolsetSearchParams) (ToolsetSearchResult, error) {
	backend, err := b.requireToolsetBackend(params.ToolsetID)
	if err != nil {
		return ToolsetSearchResult{}, err
	}
	result, err := backend.Search(ctx, toolsetctlSearchRequest(params))
	if err != nil {
		return ToolsetSearchResult{}, err
	}
	return ToolsetSearchResult{
		Packages: result.Packages,
		Tools:    result.Tools,
	}, nil
}

func (b *Bridge) inspect(ctx context.Context, params ToolsetInspectParams) (ToolsetInspectResult, error) {
	backend, err := b.requireToolsetBackend(params.ToolsetID)
	if err != nil {
		return ToolsetInspectResult{}, err
	}
	result, err := backend.Inspect(ctx, toolsetctlInspectRequest(params))
	if err != nil {
		return ToolsetInspectResult{}, err
	}
	return ToolsetInspectResult{
		Target:  result.Target,
		Version: result.Version,
		Source:  result.Source,
		Package: result.Package,
	}, nil
}

func (b *Bridge) install(ctx context.Context, params ToolsetInstallParams) (ToolsetUpdateResult, error) {
	handle, backend, err := b.requireToolsetHandleBackend(params.ToolsetID)
	if err != nil {
		return ToolsetUpdateResult{}, err
	}
	if _, err := backend.Install(ctx, toolsetctl.InstallRequest{
		Package: params.Package,
	}); err != nil {
		return ToolsetUpdateResult{}, err
	}
	return ToolsetUpdateResult{Tools: handle.toolDescriptors()}, nil
}

func (b *Bridge) uninstall(ctx context.Context, params ToolsetUninstallParams) (ToolsetUpdateResult, error) {
	handle, backend, err := b.requireToolsetHandleBackend(params.ToolsetID)
	if err != nil {
		return ToolsetUpdateResult{}, err
	}
	if _, err := backend.Uninstall(ctx, toolsetctl.UninstallRequest{
		Target: params.Target,
	}); err != nil {
		return ToolsetUpdateResult{}, err
	}
	return ToolsetUpdateResult{Tools: handle.toolDescriptors()}, nil
}

func (b *Bridge) auth(ctx context.Context, params ToolsetAuthParams) (ToolsetUpdateResult, error) {
	handle, backend, err := b.requireToolsetHandleBackend(params.ToolsetID)
	if err != nil {
		return ToolsetUpdateResult{}, err
	}
	if _, err := backend.Auth(ctx, toolsetctl.AuthRequest{
		Target:            params.Target,
		Account:           params.Account,
		Credential:        params.Credential,
		Check:             params.Check,
		DeleteCredential:  params.DeleteCredential,
		RenameAccountFrom: params.RenameAccountFrom,
		RenameAccountTo:   params.RenameAccountTo,
		DeleteAccount:     params.DeleteAccount,
	}); err != nil {
		return ToolsetUpdateResult{}, err
	}
	return ToolsetUpdateResult{Tools: handle.toolDescriptors()}, nil
}

func (b *Bridge) requireToolsetBackend(toolsetID string) (toolsetctl.ToolsetBackend, error) {
	_, backend, err := b.requireToolsetHandleBackend(toolsetID)
	if err != nil {
		return nil, err
	}
	return backend, nil
}

func (b *Bridge) requireToolsetHandleBackend(toolsetID string) (*composedToolset, toolsetctl.ToolsetBackend, error) {
	if strings.TrimSpace(toolsetID) == "" {
		return nil, nil, invalidParams("toolset_id is required")
	}
	handle, ok := b.lookupToolset(toolsetID)
	if !ok {
		return nil, nil, invalidParams(fmt.Sprintf("unknown toolset_id %q", toolsetID))
	}
	backend := handle.backendSnapshot()
	if backend == nil {
		return nil, nil, fmt.Errorf("toolset %q does not have a runtime backend", toolsetID)
	}
	return handle, backend, nil
}

func toolsetctlSearchRequest(params ToolsetSearchParams) toolpkgdiscovery.SearchRequest {
	return toolpkgdiscovery.SearchRequest{
		Query:    params.Query,
		Tools:    params.Tools,
		Packages: params.Packages,
		Runtime:  params.Runtime,
		Effect:   params.Effect,
		Limit:    params.Limit,
		Offset:   params.Offset,
	}
}

func toolsetctlInspectRequest(params ToolsetInspectParams) toolpkgdiscovery.InspectRequest {
	return toolpkgdiscovery.InspectRequest{Target: params.Target}
}

func (b *Bridge) lookupToolset(id string) (*composedToolset, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handle, ok := b.toolsets[id]
	return handle, ok
}

func (b *Bridge) ReloadFileBackedToolsets(ctx context.Context) error {
	if b == nil {
		return nil
	}

	b.mu.RLock()
	handles := make([]*composedToolset, 0, len(b.toolsets))
	for _, handle := range b.toolsets {
		handles = append(handles, handle)
	}
	b.mu.RUnlock()

	var errs []error
	for _, handle := range handles {
		backend := handle.backendSnapshot()
		reloadable, ok := backend.(reloadableToolsetBackend)
		if !ok {
			continue
		}
		if _, err := reloadable.Reload(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *Bridge) closeToolset(id string) {
	b.mu.Lock()
	handle := b.toolsets[id]
	delete(b.toolsets, id)
	b.mu.Unlock()
	b.publishPreparedTools()
	if handle != nil {
		handle.close()
	}
}

func (b *Bridge) clearToolsets() {
	b.mu.Lock()
	toolsets := b.toolsets
	b.toolsets = make(map[string]*composedToolset)
	b.mu.Unlock()
	b.publishPreparedTools()
	for _, handle := range toolsets {
		if handle != nil {
			handle.close()
		}
	}
}

func (b *Bridge) publishPreparedTools() {
	if b == nil || b.preparedToolsConsumer == nil {
		return
	}

	b.preparedToolsConsumer.SetPreparedTools(b.preparedTools())
}

func (b *Bridge) preparedTools() toolset.PreparedToolset {
	if b == nil {
		return toolset.PreparedToolset{}
	}

	b.mu.RLock()
	handles := make([]*composedToolset, 0, len(b.toolsets))
	for _, handle := range b.toolsets {
		handles = append(handles, handle)
	}
	b.mu.RUnlock()

	seen := make(map[string]struct{})
	refs := make([]string, 0)
	for _, handle := range handles {
		_, prepared, _ := handle.snapshot()
		for _, ref := range toolset.PreparedToolRefs(prepared) {
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	tools := make([]assembler.LoadedTool, 0, len(refs))
	for _, ref := range refs {
		tools = append(tools, assembler.LoadedTool{Name: ref})
	}
	return toolset.NewPreparedToolset(tools)
}

func composeCurrentDir(toolsetFile string) string {
	if strings.TrimSpace(toolsetFile) != "" {
		return filepath.Dir(toolsetFile)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
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
	return buildinfo.DisplayVersion()
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

func codeModeTBSession(manager *codemodesession.Manager, params map[string]any) (string, error) {
	if manager == nil || manager.Locked() {
		return "", nil
	}
	raw, ok := params[codemodesession.TBSessionParam]
	if !ok || raw == nil {
		return "", invalidParams("codemode tool requires params." + codemodesession.TBSessionParam)
	}
	tbSession, ok := raw.(string)
	if !ok || strings.TrimSpace(tbSession) == "" {
		return "", invalidParams("codemode tool requires params." + codemodesession.TBSessionParam)
	}
	return strings.TrimSpace(tbSession), nil
}

func codeModeTimeout(params map[string]any) (time.Duration, error) {
	raw, ok := params[codemodesession.TimeoutSecsParam]
	if !ok || raw == nil {
		return codemodesession.DefaultSubmitTimeout, nil
	}

	var seconds float64
	switch v := raw.(type) {
	case float64:
		seconds = v
	case float32:
		seconds = float64(v)
	case int:
		seconds = float64(v)
	case int8:
		seconds = float64(v)
	case int16:
		seconds = float64(v)
	case int32:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	case uint:
		seconds = float64(v)
	case uint8:
		seconds = float64(v)
	case uint16:
		seconds = float64(v)
	case uint32:
		seconds = float64(v)
	case uint64:
		seconds = float64(v)
	case json.Number:
		parsed, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a positive number", codemodesession.TimeoutSecsParam)
		}
		seconds = parsed
	default:
		return 0, fmt.Errorf("%s must be a positive number", codemodesession.TimeoutSecsParam)
	}

	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", codemodesession.TimeoutSecsParam)
	}
	timeout := time.Duration(seconds * float64(time.Second))
	if timeout <= 0 {
		return 0, fmt.Errorf("%s is too small", codemodesession.TimeoutSecsParam)
	}
	return timeout, nil
}

func cloneToolDescriptors(in []ToolDescriptor) []ToolDescriptor {
	if len(in) == 0 {
		return nil
	}
	out := make([]ToolDescriptor, len(in))
	for i := range in {
		out[i] = ToolDescriptor{
			Name:         in[i].Name,
			Description:  in[i].Description,
			ParamsSchema: cloneMap(in[i].ParamsSchema),
		}
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
