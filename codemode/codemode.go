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
	"github.com/microsoft/typescript-go/toolboxapi"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

// Run is the smallest useful codemode seam for outside-in tests.
//
// It intentionally stays narrow:
// - one resolved toolset
// - one TS code snippet
// - a stubbed SDK injected as global `tools`
// - tool calls delegated to invoke
func Run(resolved toolset.ResolvedToolset, code string) (string, error) {
	files, err := typecheckFiles(resolved, code)
	if err != nil {
		return "", err
	}

	diagnostics, err := toolboxapi.Check(context.Background(), toolboxapi.CheckInput{
		Files:            files,
		Entry:            "__codemode_run.ts",
		CurrentDirectory: "/",
	})
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
		return invoke.Run(resolved, toolName, args)
	})
	if err != nil {
		return "", fmt.Errorf("bind invoke: %w", err)
	}
	defer jsInvoke.Free()
	ctx.Global().SetPropertyStr("__invokeTool", jsInvoke)

	if _, err := rt.Eval("__codemode_tools.js", qjs.Code(preludeForTools(resolved))); err != nil {
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

func typecheckFiles(resolved toolset.ResolvedToolset, code string) (fs.FS, error) {
	files := fstest.MapFS{
		"__codemode_sdk.ts": &fstest.MapFile{Data: []byte(typecheckSDKSource(resolved))},
		"__codemode_run.ts": &fstest.MapFile{Data: []byte("import { tools } from \"./__codemode_sdk.ts\";\n" + code)},
	}

	for _, tool := range resolved.Tools() {
		if tool.TS == nil {
			continue
		}

		src, ok := tool.TS.Files.(fstest.MapFS)
		if !ok {
			return nil, fmt.Errorf("unsupported tool fs type %T", tool.TS.Files)
		}

		for name, file := range src {
			files[name] = file
		}
	}

	return files, nil
}

func preludeForTools(resolved toolset.ResolvedToolset) string {
	var b strings.Builder
	b.WriteString("const __toolboxInvoke = globalThis.__invokeTool;\n")
	b.WriteString("delete globalThis.__invokeTool;\n")
	b.WriteString("globalThis.tools = {};\n")

	seen := map[string]bool{}
	tools := resolved.Tools()
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

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

func typecheckSDKSource(resolved toolset.ResolvedToolset) string {
	var b strings.Builder
	b.WriteString("declare function __invokeTool<T>(toolName: string, args: unknown): T;\n")
	b.WriteString("type ToolResult<T> = T extends Promise<infer U> ? U : T;\n")

	tools := resolved.Tools()
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	for i, tool := range tools {
		if tool.TS == nil {
			continue
		}
		fmt.Fprintf(&b, "import * as toolmod%d from \"./%s\";\n", i, tool.TS.Entry)
	}

	namespaces := map[string][]string{}
	b.WriteString("export const tools = {\n")
	for i, tool := range tools {
		if tool.TS == nil {
			continue
		}

		parts := strings.Split(tool.Name, ".")
		if len(parts) != 2 {
			continue
		}

		namespaces[parts[0]] = append(namespaces[parts[0]], fmt.Sprintf(
			"    %s(args: Parameters<typeof toolmod%d.execute>[0]): ToolResult<ReturnType<typeof toolmod%d.execute>> { return __invokeTool<ToolResult<ReturnType<typeof toolmod%d.execute>>>(%q, args); },",
			parts[1],
			i,
			i,
			i,
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

func formatDiagnostics(diagnostics []toolboxapi.Diagnostic) string {
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
