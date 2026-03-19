package invoke

import (
	"fmt"

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
			if tool.TS != nil {
				return runTSTool(tool, args)
			}

			return "", fmt.Errorf("tool %s has no executable", tool.Name)
		}
	}

	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func runTSTool(tool tooldef.ResolvedTool, args map[string]any) (string, error) {
	if tool.Package == nil {
		return "", fmt.Errorf("tool %s has no package runtime", tool.Name)
	}

	switch tool.Package.Runtime {
	case tooldef.RuntimeTypeScriptSandbox:
		return quickts.Run(*tool.TS, args)
	case tooldef.RuntimeTypeScriptWasmerSandbox:
		return quickts.RunWithHost(*tool.TS, args, quickts.Host{
			Exec: func(binary string, execArgs []string) (quickts.ExecResult, error) {
				result, err := tswasmer.Run(tswasmer.Request{
					Binary: binary,
					Args:   execArgs,
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
	default:
		return "", fmt.Errorf("unsupported runtime for tool %s: %q", tool.Name, tool.Package.Runtime)
	}
}
