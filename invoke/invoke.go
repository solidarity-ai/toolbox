package invoke

import (
	"fmt"
	"path/filepath"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/runtime/tswasmer"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
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
	return quickts.RunWithHost(tool.TSWasm.TSToolDef, args, quickts.Host{
		Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
			relativePath, ok := tool.TSWasm.Executables[binary]
			if !ok {
				return quickts.ExecResult{}, fmt.Errorf("tool %s does not declare executable %q", tool.Name, binary)
			}

			result, err := tswasmer.Run(tswasmer.Request{
				WasmPath: filepath.Join(tool.TSWasm.PackageRoot, relativePath),
				Args:     execArgs,
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
