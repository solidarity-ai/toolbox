package codemode

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/fastschema/qjs"
	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

// sortedTools returns the tools from the view sorted by name.
func sortedTools(view toolset.AgentView) []toolset.AgentTool {
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

// groupToolsByNamespace groups tools by their dotted namespace prefix and
// returns the groups along with sorted namespace names. Tools without a
// dotted name (no namespace) are skipped.
func groupToolsByNamespace(tools []toolset.AgentTool) (map[string][]toolset.AgentTool, []string) {
	namespaces := map[string][]toolset.AgentTool{}
	for _, tool := range tools {
		parts := strings.Split(tool.Name, ".")
		if len(parts) < 2 {
			continue
		}
		ns := parts[0]
		namespaces[ns] = append(namespaces[ns], tool)
	}
	var nsNames []string
	for ns := range namespaces {
		nsNames = append(nsNames, ns)
	}
	sort.Strings(nsNames)
	return namespaces, nsNames
}

// Run is the smallest useful codemode seam for outside-in tests.
//
// It intentionally stays narrow:
// - one prepared toolset
// - one TS code snippet
// - a stubbed SDK injected as global `tools`
// - tool calls delegated to invoke
func Run(prepared toolset.PreparedToolset, code string) (string, error) {
	view := prepared.AgentView()

	files, err := typecheckFiles(prepared, code)
	if err != nil {
		return "", err
	}

	diagnostics, _, err := toolbox.Check(context.Background(), toolbox.CheckInput{
		Files:            files,
		Entry:            "__codemode_run.ts",
		CurrentDirectory: "/",
	}, nil)
	if err != nil {
		return "", fmt.Errorf("typescript check failed: %w", err)
	}
	if len(diagnostics) > 0 {
		return "", fmt.Errorf("typescript check failed: %s", formatDiagnostics(diagnostics))
	}

	rt, err := qjs.New()
	if err != nil {
		return "", fmt.Errorf("create qjs runtime: %w", err)
	}
	defer rt.Close()

	ctx := rt.Context()
	jsInvoke, err := qjs.FuncToJS(ctx, func(toolName string, args map[string]any) (string, error) {
		return invoke.Run(prepared, toolName, args)
	})
	if err != nil {
		return "", fmt.Errorf("bind invoke: %w", err)
	}
	defer jsInvoke.Free()
	ctx.Global().SetPropertyStr("__invokeTool", jsInvoke)

	if _, err := rt.Eval("__codemode_tools.js", qjs.Code(preludeForTools(view))); err != nil {
		return "", fmt.Errorf("load codemode tools: %w", err)
	}

	result := api.Transform("const tools = globalThis.tools;\n"+code, api.TransformOptions{
		Loader:     api.LoaderTS,
		Format:     api.FormatESModule,
		Target:     api.ES2023,
		Sourcefile: "codemode.ts",
		LogLevel:   api.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("transform codemode: %s", result.Errors[0].Text)
	}

	val, err := rt.Eval("codemode.js", qjs.Code(string(result.Code)), qjs.TypeModule())
	if err != nil {
		return "", fmt.Errorf("run codemode: %w", err)
	}
	defer val.Free()

	return val.String(), nil
}

func typecheckFiles(prepared toolset.PreparedToolset, code string) (fs.FS, error) {
	return fstest.MapFS{
		"__codemode_sdk.ts": &fstest.MapFile{Data: []byte(typecheckSDKSource(prepared))},
		"__codemode_run.ts": &fstest.MapFile{Data: []byte("import { tools } from \"./__codemode_sdk.ts\";\n" + code)},
	}, nil
}

func preludeForTools(view toolset.AgentView) string {
	var b strings.Builder
	// Capture the injected invoke function, remove it from globalThis to prevent
	// direct access, and initialize the tools namespace object.
	b.WriteString("const __toolboxInvoke = globalThis.__invokeTool;\n")
	b.WriteString("delete globalThis.__invokeTool;\n")
	b.WriteString("globalThis.tools = {};\n")

	seen := map[string]bool{}
	tools := sortedTools(view)

	for _, tool := range tools {
		parts := strings.Split(tool.Name, ".")
		if len(parts) == 1 {
			fmt.Fprintf(&b, "globalThis.tools[%q] = (args) => __toolboxInvoke(%q, args);\n", parts[0], tool.Name)
			continue
		}

		prefix := "globalThis.tools"
		for _, part := range parts[:len(parts)-1] {
			prefix += fmt.Sprintf("[%q]", part)
			if !seen[prefix] {
				fmt.Fprintf(&b, "%s = %s || {};\n", prefix, prefix)
				seen[prefix] = true
			}
		}

		method := parts[len(parts)-1]
		fmt.Fprintf(&b, "%s[%q] = (args) => __toolboxInvoke(%q, args);\n", prefix, method, tool.Name)
	}

	return b.String()
}

func typecheckSDKSource(prepared toolset.PreparedToolset) string {
	var b strings.Builder
	b.WriteString("declare function __invokeTool<T>(toolName: string, args: unknown): T;\n")

	view := prepared.AgentView()
	tools := sortedTools(view)

	// Collect unique type declarations from all tools' param types.
	declLines := collectUniqueDeclarations(tools)
	if len(declLines) > 0 {
		for _, line := range declLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	namespaces := map[string][]string{}
	b.WriteString("export const tools = {\n")
	for _, tool := range tools {
		parts := strings.Split(tool.Name, ".")
		if len(parts) != 2 {
			continue
		}

		paramType := "Record<string, unknown>"
		if pt := tool.ParamsType(); pt != nil {
			paramType = pt.ToTS()
		}

		namespaces[parts[0]] = append(namespaces[parts[0]], fmt.Sprintf(
			"    %s(args: %s): string { return __invokeTool<string>(%q, args); },",
			parts[1],
			paramType,
			tool.Name,
		))
	}
	var nsNames []string
	for ns := range namespaces {
		nsNames = append(nsNames, ns)
	}
	sort.Strings(nsNames)
	for _, ns := range nsNames {
		fmt.Fprintf(&b, "  %s: {\n", ns)
		for _, line := range namespaces[ns] {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("  },\n")
	}
	b.WriteString("};\n")

	return b.String()
}

func formatDiagnostics(diagnostics []toolbox.Diagnostic) string {
	parts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.File != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", diagnostic.File, diagnostic.Message))
			continue
		}
		parts = append(parts, diagnostic.Message)
	}
	return strings.Join(parts, "; ")
}
