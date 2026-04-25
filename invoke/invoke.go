package invoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
	"github.com/solidarity-ai/toolbox/vfs"
)

const defaultMaxFetchResponseBody = 10 << 20 // 10 MiB

type Executor struct {
	mu            sync.Mutex
	checkSessions map[string]*checkSessionEntry
}

type checkSessionEntry struct {
	mu      sync.Mutex
	session *toolbox.CheckSession
}

type checkSessionSpec struct {
	key string
	ts  tooldef.TSToolDef
}

var defaultExecutor = NewExecutor()

func NewExecutor(prepareds ...toolset.PreparedToolset) *Executor {
	executor := &Executor{
		checkSessions: make(map[string]*checkSessionEntry),
	}
	if len(prepareds) > 0 {
		executor.SetPrepared(prepareds[0])
	}
	return executor
}

// SetPrepared refreshes the executor's reusable checker sessions to match the
// current prepared toolset and drops cached sessions for keys that are no
// longer present. Precreation is best-effort; first real execution still
// remains the correctness path if a checker could not be prepared eagerly.
func (e *Executor) SetPrepared(prepared toolset.PreparedToolset) {
	if e == nil {
		return
	}
	specs := checkSessionSpecs(prepared)
	keep := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		keep[spec.key] = struct{}{}
	}

	e.mu.Lock()
	prepareEntries := make([]struct {
		spec  checkSessionSpec
		entry *checkSessionEntry
	}, 0, len(specs))
	for _, spec := range specs {
		prepareEntries = append(prepareEntries, struct {
			spec  checkSessionSpec
			entry *checkSessionEntry
		}{
			spec:  spec,
			entry: e.entryLocked(spec.key),
		})
	}
	var stale []*checkSessionEntry
	for key, entry := range e.checkSessions {
		if _, ok := keep[key]; ok {
			continue
		}
		delete(e.checkSessions, key)
		stale = append(stale, entry)
	}
	e.mu.Unlock()

	for _, entry := range stale {
		_ = entry.close()
	}
	for _, item := range prepareEntries {
		item.entry.prepare(item.spec.ts)
	}
}

func (e *Executor) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	entries := make([]*checkSessionEntry, 0, len(e.checkSessions))
	for key, entry := range e.checkSessions {
		delete(e.checkSessions, key)
		entries = append(entries, entry)
	}
	e.mu.Unlock()

	var closeErr error
	for _, entry := range entries {
		closeErr = errors.Join(closeErr, entry.close())
	}
	return closeErr
}

// Run selects a visible tool by name, evaluates any bindings to produce the
// full param set (including hidden params), and dispatches execution.
func Run(prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	return defaultExecutor.RunContext(context.Background(), prepared, toolName, args)
}

// RunContext is like Run but allows callers to propagate cancellation and
// deadlines into builtin tool handlers.
func RunContext(ctx context.Context, prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	return defaultExecutor.RunContext(ctx, prepared, toolName, args)
}

// Run selects a visible tool by name, evaluates any bindings to produce the
// full param set (including hidden params), and dispatches execution.
func (e *Executor) Run(prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	return e.RunContext(context.Background(), prepared, toolName, args)
}

// RunContext is like Run but allows callers to propagate cancellation and
// deadlines into builtin tool handlers.
func (e *Executor) RunContext(ctx context.Context, prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	return e.runPreparedCall(ctx, prepared, toolName, args, nil)
}

func (e *Executor) runPreparedCall(ctx context.Context, prepared toolset.PreparedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	tool, fullParams, injector, allowlist, err := prepareToolExecution(prepared, toolName, args)
	if err != nil {
		return "", err
	}
	return e.executeTool(ctx, tool, fullParams, memFS, injector, allowlist, prepared.FetchTransport())
}

// executeTool runs one already-selected tool with fully prepared params.
// It does not perform tool lookup, binding evaluation, or toolset validation.
func (e *Executor) executeTool(ctx context.Context, tool toolset.PreparedTool, fullParams map[string]any, memFS *vfs.MemFS, injector *transport.CredentialInjector, allowlist *transport.HostAllowlist, rt http.RoundTripper) (string, error) {
	if tool.BuiltIn != nil {
		return tool.BuiltIn(ctx, fullParams)
	}
	fetchFn := makeFetch(ctx, injector, allowlist, tool.MaxFetchResponseBytes(), rt)
	if tool.TSWasm != nil {
		if memFS != nil {
			return e.runTSWasmToolWithVFS(ctx, tool, fullParams, memFS, fetchFn)
		}
		return e.runTSWasmTool(ctx, tool, fullParams, fetchFn)
	}
	if tool.TS != nil {
		return e.runTSTool(ctx, tool, fullParams, fetchFn)
	}
	return "", fmt.Errorf("tool %s has no executable", tool.Name)
}

func prepareToolExecution(prepared toolset.PreparedToolset, toolName string, args map[string]any) (toolset.PreparedTool, map[string]any, *transport.CredentialInjector, *transport.HostAllowlist, error) {
	tool, err := findTool(prepared, toolName)
	if err != nil {
		return toolset.PreparedTool{}, nil, nil, nil, err
	}

	fullParams, err := tool.ValidateCall(args)
	if err != nil {
		return toolset.PreparedTool{}, nil, nil, nil, err
	}

	injector := tool.Injector()
	allowlist := tool.Allowlist()
	injector, err = tool.ScopedInjector(fullParams, injector)
	if err != nil {
		return toolset.PreparedTool{}, nil, nil, nil, err
	}

	return tool, fullParams, injector, allowlist, nil
}

func findTool(prepared toolset.PreparedToolset, toolName string) (toolset.PreparedTool, error) {
	if tool, ok := prepared.Tool(toolName); ok {
		return tool, nil
	}
	return toolset.PreparedTool{}, fmt.Errorf("unknown tool: %s", toolName)
}

func (e *Executor) runTSTool(ctx context.Context, tool toolset.PreparedTool, args map[string]any, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	return e.runWithCheckSession(tool.CacheKey(), func(session **toolbox.CheckSession) (string, error) {
		return quickts.RunWithHostContext(ctx, *tool.TS, args, quickts.Host{
			Fetch: fetchFn,
		}, session, tool.Sig)
	})
}

func (e *Executor) runWithCheckSession(key string, run func(session **toolbox.CheckSession) (string, error)) (string, error) {
	if e == nil || strings.TrimSpace(key) == "" {
		var session *toolbox.CheckSession
		return run(&session)
	}
	entry := e.entryForKey(key)
	return entry.run(run)
}

func (e *Executor) entryForKey(key string) *checkSessionEntry {
	if e == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.entryLocked(key)
}

func (e *Executor) entryLocked(key string) *checkSessionEntry {
	if e == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	entry := e.checkSessions[key]
	if entry == nil {
		entry = &checkSessionEntry{}
		e.checkSessions[key] = entry
	}
	return entry
}

func (e *checkSessionEntry) run(run func(session **toolbox.CheckSession) (string, error)) (string, error) {
	if e == nil {
		var session *toolbox.CheckSession
		return run(&session)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.session
	result, err := run(&session)
	e.replaceSessionLocked(session)
	return result, err
}

func (e *checkSessionEntry) prepare(ts tooldef.TSToolDef) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.session
	_ = quickts.PrepareCheckSession(ts, &session)
	e.replaceSessionLocked(session)
}

func (e *checkSessionEntry) replaceSessionLocked(next *toolbox.CheckSession) {
	if next == nil || next == e.session {
		return
	}
	old := e.session
	e.session = next
	if old != nil {
		_ = old.Close()
	}
}

func (e *checkSessionEntry) close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	session := e.session
	e.session = nil
	e.mu.Unlock()
	if session != nil {
		return session.Close()
	}
	return nil
}

func checkSessionSpecs(prepared toolset.PreparedToolset) []checkSessionSpec {
	seen := make(map[string]struct{})
	specs := make([]checkSessionSpec, 0, len(prepared.Tools()))
	for _, tool := range prepared.Tools() {
		if tool.Unavailable() {
			continue
		}
		if tool.TS == nil && tool.TSWasm == nil {
			continue
		}
		key := strings.TrimSpace(tool.CacheKey())
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		spec := checkSessionSpec{key: key}
		switch {
		case tool.TS != nil:
			spec.ts = *tool.TS
		case tool.TSWasm != nil:
			spec.ts = tool.TSWasm.TSToolDef
		}
		specs = append(specs, spec)
	}
	return specs
}

// RunWithVFS is like Run but accepts an existing MemFS for the shared VFS.
// This allows callers to pre-populate files before execution and inspect
// files written by the WASM guest afterwards.
func RunWithVFS(prepared toolset.PreparedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	return defaultExecutor.RunWithVFS(prepared, toolName, args, memFS)
}

// RunWithVFS is like Run but accepts an existing MemFS for the shared VFS.
// This allows callers to pre-populate files before execution and inspect
// files written by the WASM guest afterwards.
func (e *Executor) RunWithVFS(prepared toolset.PreparedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	return e.runPreparedCall(context.Background(), prepared, toolName, args, memFS)
}

func (e *Executor) runTSWasmTool(ctx context.Context, tool toolset.PreparedTool, args map[string]any, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	return e.runTSWasmToolWithVFS(ctx, tool, args, vfs.NewMemFS(), fetchFn)
}

func (e *Executor) runTSWasmToolWithVFS(ctx context.Context, tool toolset.PreparedTool, args map[string]any, memFS *vfs.MemFS, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	sockPath, cleanup, err := startVFSServer(memFS)
	if err != nil {
		return "", fmt.Errorf("start vfs server: %w", err)
	}
	defer cleanup()

	return e.runWithCheckSession(tool.CacheKey(), func(session **toolbox.CheckSession) (string, error) {
		return quickts.RunWithHostContext(ctx, tool.TSWasm.TSToolDef, args, quickts.Host{
			ReadFile: func(path string) (string, error) {
				data, err := memFS.ReadAll(path)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
			WriteFile: func(path string, data string) error {
				return memFS.WriteFile(path, []byte(data))
			},
			Fetch: fetchFn,
			Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
				relativePath, ok := tool.TSWasm.Executables[binary]
				if !ok {
					return quickts.ExecResult{}, fmt.Errorf("tool %s does not declare executable %q", tool.Name, binary)
				}

				req := tswasmcli.Request{
					Args:        execArgs,
					Runtime:     runtimeFlag(tool.PackageMeta.Runtime),
					VFSSockPath: sockPath,
				}

				if tool.TSWasm.PackageRoot != "" {
					req.WasmPath = filepath.Join(tool.TSWasm.PackageRoot, relativePath)
				} else {
					wasmBytes, err := fs.ReadFile(tool.TSWasm.Files, relativePath)
					if err != nil {
						return quickts.ExecResult{}, fmt.Errorf("read wasm %q from archive: %w", binary, err)
					}
					req.WasmBytes = wasmBytes
				}

				result, err := tswasmcli.RunContext(ctx, req)
				if err != nil {
					return quickts.ExecResult{}, err
				}

				return quickts.ExecResult{
					Stdout:   result.Stdout,
					Stderr:   result.Stderr,
					ExitCode: result.ExitCode,
				}, nil
			},
		}, session, tool.Sig)
	})
}

// startVFSServer creates a UDS, starts the VFS server goroutine, and returns
// the socket path and a cleanup function.
func startVFSServer(memFS *vfs.MemFS) (string, func(), error) {
	sockFile, err := os.CreateTemp(os.TempDir(), "toolbox-vfs-*.sock")
	if err != nil {
		return "", nil, fmt.Errorf("allocate vfs socket path: %w", err)
	}
	sockPath := sockFile.Name()
	if err := sockFile.Close(); err != nil {
		_ = os.Remove(sockPath)
		return "", nil, fmt.Errorf("close vfs socket placeholder %s: %w", sockPath, err)
	}
	if err := os.Remove(sockPath); err != nil {
		return "", nil, fmt.Errorf("remove vfs socket placeholder %s: %w", sockPath, err)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return "", nil, fmt.Errorf("listen on %s: %w", sockPath, err)
	}

	srv := vfs.NewServer(memFS, listener)
	go srv.Serve()

	cleanup := func() {
		srv.Close()
		os.Remove(sockPath)
	}

	return sockPath, cleanup, nil
}

func runtimeFlag(rt tooldef.ToolRuntime) string {
	switch rt {
	case tooldef.RuntimeTypeScriptWasip2Sandbox:
		return "wasip2-cli"
	default:
		return "wasix-cli"
	}
}

// makeFetch returns a fetch function that optionally injects credentials
// and enforces a host allowlist.
func makeFetch(ctx context.Context, injector *transport.CredentialInjector, allowlist *transport.HostAllowlist, maxResponseBodyBytes *int64, rt http.RoundTripper) func(string, string, string, string) (quickts.FetchResult, error) {
	if injector == nil && allowlist == nil && rt == nil {
		return func(rawURL, method, headersJSON, body string) (quickts.FetchResult, error) {
			return goFetch(ctx, rawURL, method, headersJSON, body, maxResponseBodyBytes)
		}
	}
	return func(rawURL, method, headersJSON, body string) (quickts.FetchResult, error) {
		var applied *transport.AppliedInjection
		prepareRequest := func(req *http.Request, via []*http.Request) error {
			if allowlist != nil && !allowlist.Allows(req.URL.Hostname()) {
				return fmt.Errorf("host %s not in allowlist", req.URL.Hostname())
			}
			if applied != nil {
				applied.Remove(req)
				applied = nil
			}
			if isHTTPSDowngrade(req, via) {
				return nil
			}
			if injector == nil {
				return nil
			}
			next, err := injector.Apply(req)
			if err != nil {
				return fmt.Errorf("credential injection failed")
			}
			applied = next
			return nil
		}
		return goFetchWithAllowlist(ctx, rawURL, method, headersJSON, body, prepareRequest, maxResponseBodyBytes, rt)
	}
}

// goFetch performs an HTTP request using the fetch package.
// It's the Go-side implementation behind the JS fetch() global.
func goFetch(ctx context.Context, rawURL, method, headersJSON, body string, maxResponseBodyBytes *int64) (quickts.FetchResult, error) {
	return goFetchWithAllowlist(ctx, rawURL, method, headersJSON, body, nil, maxResponseBodyBytes, nil)
}

// goFetchWithAllowlist is like goFetch but allows the caller to prepare the
// initial request and each redirected request before they are sent.
func goFetchWithAllowlist(ctx context.Context, rawURL, method, headersJSON, body string, prepareRequest func(*http.Request, []*http.Request) error, maxResponseBodyBytes *int64, rt http.RoundTripper) (quickts.FetchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqHeaders := fetch.NewHeaders()
	var pairs [][2]string
	if err := json.Unmarshal([]byte(headersJSON), &pairs); err == nil {
		for _, p := range pairs {
			reqHeaders.Append(p[0], p[1])
		}
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	init := &fetch.RequestInit{
		Method:         method,
		Headers:        reqHeaders,
		Body:           bodyReader,
		PrepareRequest: prepareRequest,
		Transport:      rt,
	}

	resp, err := fetch.Fetch(ctx, rawURL, init)
	if err != nil {
		return quickts.FetchResult{}, err
	}
	defer resp.Body().Close()

	limit := int64(defaultMaxFetchResponseBody)
	if maxResponseBodyBytes != nil {
		limit = *maxResponseBodyBytes
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body(), limit+1))
	if err != nil {
		return quickts.FetchResult{}, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(respBody)) > limit {
		return quickts.FetchResult{}, fmt.Errorf("response body exceeds %d bytes", limit)
	}

	return quickts.FetchResult{
		Status:     resp.Status(),
		StatusText: resp.StatusText(),
		Headers:    resp.Headers().Entries(),
		Body:       string(respBody),
		URL:        resp.URL(),
	}, nil
}

func isHTTPSDowngrade(req *http.Request, via []*http.Request) bool {
	if len(via) == 0 {
		return false
	}
	prev := via[len(via)-1]
	return strings.EqualFold(prev.URL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http")
}
