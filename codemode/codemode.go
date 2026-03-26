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
	tooldef "github.com/solidarity-ai/toolbox/tool"
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

			hidden := tool.HiddenParams()
			literals := tool.BoundLiterals()

			// Build the access mode label suffix for the JSDoc description.
			modeLabel := accessModeLabel(tool.AccessMode, tool.Idempotent)

			// Collect multi-line param descriptions for @param tags.
			var multiLineParams []struct {
				name string
				desc string
			}
			if tool.Sig != nil {
				for _, p := range tool.Sig.Params() {
					if hidden[p.Name()] {
						continue
					}
					desc := p.Description()
					if desc != "" && strings.Contains(desc, "\n") {
						multiLineParams = append(multiLineParams, struct {
							name string
							desc string
						}{name: p.Name(), desc: desc})
					}
				}
			}

			// JSDoc block: description + access mode label + multi-line @param tags.
			if tool.Sig != nil {
				desc := tool.Sig.Description()
				if len(multiLineParams) > 0 {
					// Multi-line JSDoc block.
					b.WriteString("    /**\n")
					if desc != "" {
						descLine := desc
						if modeLabel != "" {
							descLine += " " + modeLabel
						}
						fmt.Fprintf(&b, "     * %s\n", descLine)
					} else if modeLabel != "" {
						fmt.Fprintf(&b, "     * %s\n", modeLabel)
					}
					for _, mp := range multiLineParams {
						lines := strings.Split(mp.desc, "\n")
						fmt.Fprintf(&b, "     * @param %s - %s\n", mp.name, lines[0])
						for _, line := range lines[1:] {
							fmt.Fprintf(&b, "     *   %s\n", line)
						}
					}
					b.WriteString("     */\n")
				} else if desc != "" {
					descLine := desc
					if modeLabel != "" {
						descLine += " " + modeLabel
					}
					fmt.Fprintf(&b, "    /** %s */\n", descLine)
				} else if modeLabel != "" {
					fmt.Fprintf(&b, "    /** %s */\n", modeLabel)
				}
			}

			// Build individual param list.
			var paramParts []string
			if tool.Sig != nil {
				for _, p := range tool.Sig.Params() {
					if hidden[p.Name()] {
						continue
					}
					// Determine param type: use bound literal if available,
					// otherwise use the param's TypeScript type.
					var tsType string
					if litVal, ok := literals[p.Name()]; ok {
						tsType = literalToTS(litVal)
					} else {
						tsType = p.Type().ToTS()
					}
					name := p.Name()
					if p.Optional() {
						name += "?"
					}

					// Add inline comment for single-line param descriptions.
					desc := p.Description()
					if desc != "" && !strings.Contains(desc, "\n") {
						paramParts = append(paramParts, fmt.Sprintf("/** %s */ %s: %s", desc, name, tsType))
					} else {
						paramParts = append(paramParts, fmt.Sprintf("%s: %s", name, tsType))
					}
				}
			}

			returnType := "string"
			if tool.Sig != nil {
				if rt := tool.Sig.Return(); rt != nil {
					returnType = rt.UnwrapPromise().ToTS()
				}
			}

			fmt.Fprintf(&b, "    %s(%s): %s;\n", method, strings.Join(paramParts, ", "), returnType)
		}
		b.WriteString("  };\n")
	}
	b.WriteString("};\n")

	return b.String()
}

// accessModeLabel returns the parenthesized label for the access mode and
// idempotent flag, or "" if no access mode is set.
func accessModeLabel(mode tooldef.AccessMode, idempotent *bool) string {
	isIdempotent := idempotent != nil && *idempotent
	switch mode {
	case tooldef.AccessModeReadOnly:
		return "(readonly)"
	case tooldef.AccessModeReversible:
		if isIdempotent {
			return "(reversible, idempotent)"
		}
		return "(reversible)"
	case tooldef.AccessModeIrreversible:
		if isIdempotent {
			return "(irreversible, idempotent)"
		}
		return "(irreversible)"
	default:
		return ""
	}
}

// literalToTS formats a Go value as a TypeScript literal type.
func literalToTS(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", val)
	}
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
