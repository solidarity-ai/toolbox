package invoke

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
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
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
	"github.com/solidarity-ai/toolbox/vfs"
)

const (
	tswasmProxyHost            = "proxy.toolbox.internal"
	tswasmProxyGuestEtcPath    = "/etc"
	tswasmProxyGuestHostsPath  = "/etc/hosts"
	tswasmProxyGuestCACertPath = "/etc/ssl/certs/ca-certificates.crt"
)

var (
	checkSessionsMu sync.RWMutex
	checkSessions   = map[*tooldef.Package]*toolbox.CheckSession{}

	runTSWasmCLI = tswasmcli.Run
	newMITMProxy = func() (*mitmproxy.Proxy, error) {
		return mitmproxy.New(nil)
	}
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
func Run(resolved toolset.ResolvedToolset, toolName string, args map[string]any) (string, error) {
	fullParams, err := resolved.ValidateCall(toolName, args)
	if err != nil {
		return "", err
	}

	for _, tool := range resolved.Tools() {
		if tool.Name == toolName {
			transportPolicy, _ := resolved.ToolTransportPolicy(tool.Name)
			if tool.TSWasm != nil {
				return runTSWasmTool(tool, fullParams, transportPolicy)
			}
			if tool.TS != nil {
				return runTSTool(tool, fullParams, transportPolicy)
			}

			return "", fmt.Errorf("tool %s has no executable", tool.Name)
		}
	}

	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runTSTool(tool tooldef.ResolvedTool, args map[string]any, transportPolicy *transport.Policy) (string, error) {
	session := getCheckSession(tool.Package)
	result, err := quickts.RunWithHost(*tool.TS, args, quickts.Host{
		Fetch: goFetchWithTransportPolicy(transportPolicy),
	}, &session, tool.Sig)
	setCheckSession(tool.Package, session)
	return result, err
}

// RunWithVFS is like Run but accepts an existing MemFS for the shared VFS.
// This allows callers to pre-populate files before execution and inspect
// files written by the WASM guest afterwards.
func RunWithVFS(resolved toolset.ResolvedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	fullParams, err := resolved.ValidateCall(toolName, args)
	if err != nil {
		return "", err
	}

	for _, tool := range resolved.Tools() {
		if tool.Name == toolName {
			transportPolicy, _ := resolved.ToolTransportPolicy(tool.Name)
			if tool.TSWasm != nil {
				return runTSWasmToolWithVFS(tool, fullParams, memFS, transportPolicy)
			}
			if tool.TS != nil {
				return runTSTool(tool, fullParams, transportPolicy)
			}
			return "", fmt.Errorf("tool %s has no executable", tool.Name)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runTSWasmTool(tool tooldef.ResolvedTool, args map[string]any, transportPolicy *transport.Policy) (string, error) {
	return runTSWasmToolWithVFS(tool, args, vfs.NewMemFS(), transportPolicy)
}

func runTSWasmToolWithVFS(tool tooldef.ResolvedTool, args map[string]any, memFS *vfs.MemFS, transportPolicy *transport.Policy) (string, error) {
	sockPath, cleanup, err := startVFSServer(memFS)
	if err != nil {
		return "", fmt.Errorf("start vfs server: %w", err)
	}
	defer cleanup()

	session := getCheckSession(tool.Package)
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
		Fetch: goFetchWithTransportPolicy(transportPolicy),
		Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
			return runTSWasmExecutable(tool, binary, execArgs, sockPath, transportPolicy)
		},
	}, &session, tool.Sig)
	setCheckSession(tool.Package, session)
	return result, err
}

func runTSWasmExecutable(tool tooldef.ResolvedTool, binary string, execArgs []string, sockPath string, transportPolicy *transport.Policy) (quickts.ExecResult, error) {
	req, cleanup, err := buildTSWasmRequest(tool, binary, execArgs, sockPath, transportPolicy)
	if err != nil {
		return quickts.ExecResult{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	result, err := runTSWasmCLI(req)
	if err != nil {
		return quickts.ExecResult{}, err
	}

	return quickts.ExecResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}, nil
}

func buildTSWasmRequest(tool tooldef.ResolvedTool, binary string, execArgs []string, sockPath string, transportPolicy *transport.Policy) (tswasmcli.Request, func(), error) {
	relativePath, ok := tool.TSWasm.Executables[binary]
	if !ok {
		return tswasmcli.Request{}, nil, fmt.Errorf("tool %s does not declare executable %q", tool.Name, binary)
	}

	req := tswasmcli.Request{
		Args:        execArgs,
		Runtime:     runtimeFlag(tool.Package.Runtime),
		VFSSockPath: sockPath,
	}

	if tool.TSWasm.PackageRoot != "" {
		req.WasmPath = filepath.Join(tool.TSWasm.PackageRoot, relativePath)
	} else {
		wasmBytes, err := fs.ReadFile(tool.TSWasm.Files, relativePath)
		if err != nil {
			return tswasmcli.Request{}, nil, fmt.Errorf("read wasm %q from archive: %w", binary, err)
		}
		req.WasmBytes = wasmBytes
	}

	proxyConfig, err := startTSWasmProxyExecution(transportPolicy)
	if err != nil {
		return tswasmcli.Request{}, nil, err
	}
	if proxyConfig != nil {
		req.Env = proxyConfig.Env
		req.Mounts = proxyConfig.Mounts
		return req, proxyConfig.Cleanup, nil
	}

	return req, nil, nil
}

type tswasmProxyExecution struct {
	Env     map[string]string
	Mounts  []tswasmcli.Mount
	Cleanup func()
}

func startTSWasmProxyExecution(policy *transport.Policy) (_ *tswasmProxyExecution, err error) {
	if policy == nil {
		return nil, nil
	}

	proxy, err := newMITMProxy()
	if err != nil {
		return nil, fmt.Errorf("start mitm proxy: %w", err)
	}
	proxy.Policy = policy

	listener, err := proxy.ListenAndServe()
	if err != nil {
		return nil, fmt.Errorf("start mitm proxy listener: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "toolbox-tswasm-proxy-*")
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("create proxy support dir: %w", err)
	}

	cleanup := func() {
		listener.Close()
		_ = os.RemoveAll(tempDir)
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err = writeTSWasmProxySupportFiles(tempDir); err != nil {
		return nil, fmt.Errorf("write proxy support files: %w", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return nil, fmt.Errorf("resolve proxy listener address: %w", err)
	}

	proxyURL := fmt.Sprintf("http://%s:%s", tswasmProxyHost, port)
	return &tswasmProxyExecution{
		Env: map[string]string{
			"HTTPS_PROXY":   proxyURL,
			"HTTP_PROXY":    proxyURL,
			"SSL_CERT_FILE": tswasmProxyGuestCACertPath,
			"NO_PROXY":      "",
			"no_proxy":      "",
		},
		Mounts: []tswasmcli.Mount{{
			HostPath:  tempDir,
			GuestPath: tswasmProxyGuestEtcPath,
		}},
		Cleanup: cleanup,
	}, nil
}

func writeTSWasmProxySupportFiles(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "ssl", "certs"), 0o755); err != nil {
		return fmt.Errorf("create ssl certs dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "hosts"), []byte("127.0.0.1 localhost "+tswasmProxyHost+"\n::1 localhost\n"), 0o644); err != nil {
		return fmt.Errorf("write hosts file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ssl", "certs", "ca-certificates.crt"), mitmproxy.CACertPEM(), 0o644); err != nil {
		return fmt.Errorf("write ca cert: %w", err)
	}
	return nil
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

// goFetchWithTransportPolicy performs an HTTP request using the fetch package.
// It's the Go-side implementation behind the JS fetch() global.
func goFetchWithTransportPolicy(policy *transport.Policy) func(url, method, headersJSON, body string) (quickts.FetchResult, error) {
	return func(url, method, headersJSON, body string) (quickts.FetchResult, error) {
		reqHeaders := fetch.NewHeaders()
		var pairs [][2]string
		if err := json.Unmarshal([]byte(headersJSON), &pairs); err == nil {
			for _, p := range pairs {
				reqHeaders.Append(p[0], p[1])
			}
		}

		var err error
		if policy != nil {
			url, err = policy.PrepareRequest(context.Background(), url, reqHeaders)
			if err != nil {
				return quickts.FetchResult{}, err
			}
		}

		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}

		resp, err := fetch.Fetch(context.Background(), url, &fetch.RequestInit{
			Method:  method,
			Headers: reqHeaders,
			Body:    bodyReader,
		})
		if err != nil {
			return quickts.FetchResult{}, err
		}
		defer resp.Body().Close()

		respBody, err := io.ReadAll(resp.Body())
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
}
