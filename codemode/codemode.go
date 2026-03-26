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

// Run is the smallest useful codemode seam for outside-in tests.
//
// It intentionally stays narrow:
// - one resolved toolset
// - one TS code snippet
// - a stubbed SDK injected as global `tools`
// - tool calls delegated to invoke
func Run(resolved toolset.ResolvedToolset, code string) (string, error) {
	view := resolved.AgentView()

	files, err := typecheckFiles(resolved, code)
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
		return invoke.Run(resolved, toolName, args)
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

func typecheckFiles(resolved toolset.ResolvedToolset, code string) (fs.FS, error) {
	return fstest.MapFS{
		"__codemode_sdk.ts": &fstest.MapFile{Data: []byte(typecheckSDKSource(resolved))},
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
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
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

	view := resolved.AgentView()
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	// Emit type declarations for any $ref definitions
	for _, tool := range tools {
		if pt := tool.ParamsType(); pt != nil {
			decls := pt.Declarations()
			if decls != "" {
				b.WriteString(decls)
				b.WriteString("\n")
			}
		}
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

// DeclarationSource generates a .d.ts file with JSDoc comments for the
// agent-visible tools. This is the type declaration the agent imports.
func DeclarationSource(resolved toolset.ResolvedToolset) string {
	var b strings.Builder

	view := resolved.AgentView()
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	// Emit type declarations for any $ref definitions (params and return types).
	for _, tool := range tools {
		if pt := tool.ParamsType(); pt != nil {
			if decls := pt.Declarations(); decls != "" {
				b.WriteString(decls)
				b.WriteString("\n")
			}
		}
		if tool.Sig != nil {
			if rt := tool.Sig.Return(); rt != nil {
				if decls := rt.Declarations(); decls != "" {
					b.WriteString(decls)
					b.WriteString("\n")
				}
			}
		}
	}

	namespaces := map[string][]toolset.AgentTool{}
	for _, tool := range tools {
		parts := strings.Split(tool.Name, ".")
		if len(parts) != 2 {
			continue
		}
		namespaces[parts[0]] = append(namespaces[parts[0]], tool)
	}
	var nsNames []string
	for ns := range namespaces {
		nsNames = append(nsNames, ns)
	}
	sort.Strings(nsNames)

	b.WriteString("export declare const tools: {\n")
	for _, ns := range nsNames {
		fmt.Fprintf(&b, "  %s: {\n", ns)
		for _, tool := range namespaces[ns] {
			parts := strings.Split(tool.Name, ".")
			method := parts[len(parts)-1]

			// Build set of hidden params to exclude from @param tags.
			hidden := tool.HiddenParams()

			// Emit JSDoc with description, visible @param tags, and @returns.
			if tool.Sig != nil {
				desc := tool.Sig.Description()
				params := tool.Sig.Params()
				hasContent := desc != ""
				for _, p := range params {
					if !hidden[p.Name()] && p.Description() != "" {
						hasContent = true
						break
					}
				}
				if hasContent {
					b.WriteString("    /**\n")
					if desc != "" {
						fmt.Fprintf(&b, "     * %s\n", desc)
					}
					for _, p := range params {
						if hidden[p.Name()] {
							continue
						}
						if d := p.Description(); d != "" {
							fmt.Fprintf(&b, "     * @param %s - %s\n", p.Name(), d)
						}
					}
					b.WriteString("     */\n")
				}
			}

			paramType := "Record<string, unknown>"
			if pt := tool.ParamsType(); pt != nil {
				paramType = pt.ToTS()
			}

			returnType := "string"
			if tool.Sig != nil {
				if rt := tool.Sig.Return(); rt != nil {
					returnType = rt.ToTS()
				}
			}

			fmt.Fprintf(&b, "    %s(args: %s): %s;\n", method, paramType, returnType)
		}
		b.WriteString("  };\n")
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
