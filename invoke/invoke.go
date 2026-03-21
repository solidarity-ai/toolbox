package invoke

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/runtime/tswasixcli"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
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

// Run is the minimal invoke seam for the first outside-in tests.
//
// For now it only proves that a caller can select one visible tool by name and
// route execution through a single package boundary.
func Run(resolved toolset.ResolvedToolset, toolName string, args map[string]any) (string, error) {
	for _, tool := range resolved.Tools() {
		if tool.Name == toolName {
			if tool.TSWasm != nil {
				return runTSWasmTool(tool, args)
			}
			if tool.TS != nil {
				return runTSTool(tool, args)
			}

			return "", fmt.Errorf("tool %s has no executable", tool.Name)
		}
	}

	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runTSTool(tool tooldef.ResolvedTool, args map[string]any) (string, error) {
	session := getCheckSession(tool.Package)
	result, err := quickts.RunWithHost(*tool.TS, args, quickts.Host{}, &session)
	setCheckSession(tool.Package, session)
	return result, err
}

// RunWithVFS is like Run but accepts an existing MemFS for the shared VFS.
// This allows callers to pre-populate files before execution and inspect
// files written by the WASM guest afterwards.
func RunWithVFS(resolved toolset.ResolvedToolset, toolName string, args map[string]any, memFS *vfs.MemFS) (string, error) {
	for _, tool := range resolved.Tools() {
		if tool.Name == toolName {
			if tool.TSWasm != nil {
				return runTSWasmToolWithVFS(tool, args, memFS)
			}
			if tool.TS != nil {
				return runTSTool(tool, args)
			}
			return "", fmt.Errorf("tool %s has no executable", tool.Name)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runTSWasmTool(tool tooldef.ResolvedTool, args map[string]any) (string, error) {
	return runTSWasmToolWithVFS(tool, args, vfs.NewMemFS())
}

func runTSWasmToolWithVFS(tool tooldef.ResolvedTool, args map[string]any, memFS *vfs.MemFS) (string, error) {
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
		Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
			relativePath, ok := tool.TSWasm.Executables[binary]
			if !ok {
				return quickts.ExecResult{}, fmt.Errorf("tool %s does not declare executable %q", tool.Name, binary)
			}

			req := tswasixcli.Request{
				Args:        execArgs,
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

			result, err := tswasixcli.Run(req)
			if err != nil {
				return quickts.ExecResult{}, err
			}

			return quickts.ExecResult{
				Stdout:   result.Stdout,
				Stderr:   result.Stderr,
				ExitCode: result.ExitCode,
			}, nil
		},
	}, &session)
	setCheckSession(tool.Package, session)
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
