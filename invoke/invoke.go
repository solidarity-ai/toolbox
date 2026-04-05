package invoke

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
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

var (
	checkSessionsMu sync.RWMutex
	checkSessions   = map[*tooldef.Package]*toolbox.CheckSession{}
)

func getCheckSession(pkg *tooldef.Package) *toolbox.CheckSession {
	checkSessionsMu.RLock()
	defer checkSessionsMu.RUnlock()
	return checkSessions[pkg]
}

func setCheckSession(pkg *tooldef.Package, session *toolbox.CheckSession) {
	checkSessionsMu.Lock()
	defer checkSessionsMu.Unlock()
	checkSessions[pkg] = session
}

// Run selects a visible tool by name, evaluates any bindings to produce the
// full param set (including hidden params), and dispatches execution.
func Run(prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	tool, fullParams, injector, allowlist, err := prepareToolExecution(prepared, toolName, args)
	if err != nil {
		return "", err
	}
	return executeTool(tool, fullParams, nil, injector, allowlist)
}

// executeTool runs one already-selected tool with fully prepared params.
// It does not perform tool lookup, binding evaluation, or toolset validation.
func executeTool(tool toolset.PreparedTool, fullParams map[string]any, memFS *vfs.MemFS, injector *transport.CredentialInjector, allowlist *transport.HostAllowlist) (string, error) {
	fetchFn := makeFetch(injector, allowlist)
	if tool.TSWasm != nil {
		if memFS != nil {
			return runTSWasmToolWithVFS(tool, fullParams, memFS, fetchFn)
		}
		return runTSWasmTool(tool, fullParams, fetchFn)
	}
	if tool.TS != nil {
		return runTSTool(tool, fullParams, fetchFn)
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

func runTSTool(tool toolset.PreparedTool, args map[string]any, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	session := getCheckSession(tool.PackageMeta)
	result, err := quickts.RunWithHost(*tool.TS, args, quickts.Host{
		Fetch: fetchFn,
	}, &session, tool.Sig)
	setCheckSession(tool.PackageMeta, session)
	return result, err
}

// RunWithVFS is like Run but accepts an existing MemFS for the shared VFS.
// This allows callers to pre-populate files before execution and inspect
// files written by the WASM guest afterwards.
func RunWithVFS(prepared toolset.PreparedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	tool, fullParams, injector, allowlist, err := prepareToolExecution(prepared, toolName, args)
	if err != nil {
		return "", err
	}
	return executeTool(tool, fullParams, memFS, injector, allowlist)
}

func runTSWasmTool(tool toolset.PreparedTool, args map[string]any, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	return runTSWasmToolWithVFS(tool, args, vfs.NewMemFS(), fetchFn)
}

func runTSWasmToolWithVFS(tool toolset.PreparedTool, args map[string]any, memFS *vfs.MemFS, fetchFn func(string, string, string, string) (quickts.FetchResult, error)) (string, error) {
	sockPath, cleanup, err := startVFSServer(memFS)
	if err != nil {
		return "", fmt.Errorf("start vfs server: %w", err)
	}
	defer cleanup()

	session := getCheckSession(tool.PackageMeta)
	result, err := quickts.RunWithHost(tool.TSWasm.TSToolDef, args, quickts.Host{
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

			result, err := tswasmcli.Run(req)
			if err != nil {
				return quickts.ExecResult{}, err
			}

			return quickts.ExecResult{
				Stdout:   result.Stdout,
				Stderr:   result.Stderr,
				ExitCode: result.ExitCode,
			}, nil
		},
	}, &session, tool.Sig)
	setCheckSession(tool.PackageMeta, session)
	return result, err
}

// startVFSServer creates a UDS, starts the VFS server goroutine, and returns
// the socket path and a cleanup function.
func startVFSServer(memFS *vfs.MemFS) (string, func(), error) {
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("toolbox-vfs-%d.sock", os.Getpid()))
	os.Remove(sockPath) // best-effort cleanup of stale socket

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
func makeFetch(injector *transport.CredentialInjector, allowlist *transport.HostAllowlist) func(string, string, string, string) (quickts.FetchResult, error) {
	if injector == nil && allowlist == nil {
		return goFetch
	}
	return func(rawURL, method, headersJSON, body string) (quickts.FetchResult, error) {
		// Check allowlist FIRST — before credential injection to avoid side effects.
		if allowlist != nil {
			host := extractHost(rawURL)
			if !allowlist.Allows(host) {
				return quickts.FetchResult{}, fmt.Errorf("host %s not in allowlist", host)
			}
		}
		newURL := rawURL
		newHeadersJSON := headersJSON
		if injector != nil {
			var pairs [][2]string
			json.Unmarshal([]byte(headersJSON), &pairs) //nolint: errcheck — empty on failure is fine
			var err error
			var newHeaders [][2]string
			newURL, newHeaders, _, err = injector.InjectRequest(method, rawURL, pairs)
			if err != nil {
				return quickts.FetchResult{}, fmt.Errorf("credential injection failed")
			}
			injected, _ := json.Marshal(newHeaders)
			newHeadersJSON = string(injected)
		}
		var strip func(*http.Request, []*http.Request)
		if injector != nil {
			strip = injector.StripRedirected
		}
		return goFetchWithAllowlist(newURL, method, newHeadersJSON, body, allowlist, strip)
	}
}

// extractHost parses a URL and returns the hostname (without port).
func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// goFetch performs an HTTP request using the fetch package.
// It's the Go-side implementation behind the JS fetch() global.
func goFetch(rawURL, method, headersJSON, body string) (quickts.FetchResult, error) {
	return goFetchWithAllowlist(rawURL, method, headersJSON, body, nil, nil)
}

// goFetchWithAllowlist is like goFetch but enforces a host allowlist on redirect hops
// and strips injected credentials when redirect targets are no longer trusted.
func goFetchWithAllowlist(rawURL, method, headersJSON, body string, allowlist *transport.HostAllowlist, stripInjected func(*http.Request, []*http.Request)) (quickts.FetchResult, error) {
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
		Method:  method,
		Headers: reqHeaders,
		Body:    bodyReader,
	}
	if allowlist != nil || stripInjected != nil {
		init.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if allowlist != nil && !allowlist.Allows(req.URL.Hostname()) {
				return fmt.Errorf("redirect to %s blocked by allowlist", req.URL.Hostname())
			}
			if stripInjected != nil && len(via) > 0 {
				stripInjected(req, via)
			}
			return nil
		}
	}

	resp, err := fetch.Fetch(context.Background(), rawURL, init)
	if err != nil {
		return quickts.FetchResult{}, err
	}
	defer resp.Body().Close()

	const maxResponseBody = 10 << 20 // 10 MB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body(), maxResponseBody))
	if err != nil {
		return quickts.FetchResult{}, fmt.Errorf("read response body: %w", err)
	}

	return quickts.FetchResult{
		Status:     resp.Status(),
		StatusText: resp.StatusText(),
		Headers:    resp.Headers().Entries(),
		Body:       string(respBody),
		URL:        resp.URL(),
	}, nil
}
