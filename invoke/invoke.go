package invoke

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/runtime/tswasmer"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/vfs"
)

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
	return quickts.Run(*tool.TS, args)
}

func runTSWasmTool(tool tooldef.ResolvedTool, args map[string]any) (string, error) {
	// Create a shared VFS for this execution.
	memFS := vfs.NewMemFS()
	sockPath, cleanup, err := startVFSServer(memFS)
	if err != nil {
		return "", fmt.Errorf("start vfs server: %w", err)
	}
	defer cleanup()

	return quickts.RunWithHost(tool.TSWasm.TSToolDef, args, quickts.Host{
		Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
			relativePath, ok := tool.TSWasm.Executables[binary]
			if !ok {
				return quickts.ExecResult{}, fmt.Errorf("tool %s does not declare executable %q", tool.Name, binary)
			}

			result, err := tswasmer.Run(tswasmer.Request{
				WasmPath:    filepath.Join(tool.TSWasm.PackageRoot, relativePath),
				Args:        execArgs,
				VFSSockPath: sockPath,
			})
			if err != nil {
				return quickts.ExecResult{}, err
			}

			return quickts.ExecResult{
				Stdout:   result.Stdout,
				Stderr:   result.Stderr,
				ExitCode: result.ExitCode,
			}, nil
		},
	})
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
