package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing/fstest"
	"unicode"

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

	// Collect unique type declarations from all tools' param and return types.
	// Each declaration line (e.g. "type Foo = ...;") is deduplicated so that
	// shared types referenced by multiple tools are emitted exactly once.
	declLines := collectUniqueDeclarations(tools)
	if len(declLines) > 0 {
		for _, line := range declLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
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

	// Analyze return types: decide rendering mode for each tool and detect
	// shared return types across tools.
	type returnTypeInfo struct {
		mode     string // "named", "inline-comments", "plain"
		typeName string // for "named" mode
	}
	returnTypes := map[string]returnTypeInfo{} // keyed by tool.Name

	// Map from canonical JSON representation of return schema -> first tool method name that uses it.
	// Used to detect shared return types.
	type sharedInfo struct {
		typeName   string
		properties []returnPropInfo
	}
	sharedReturnTypes := map[string]*sharedInfo{}

	for _, tool := range tools {
		if tool.Sig == nil {
			continue
		}
		rt := tool.Sig.Return()
		if rt == nil {
			continue
		}
		unwrapped := rt.UnwrapPromise()
		if !unwrapped.IsObject() {
			continue
		}
		propNames := unwrapped.PropertyNames()
		if len(propNames) == 0 {
			continue
		}

		props := returnTypeProperties(unwrapped)
		if len(props) == 0 {
			continue
		}

		// Check if any property has a description.
		hasDesc := false
		hasMultiLineDesc := false
		for _, p := range props {
			if p.description != "" {
				hasDesc = true
				if strings.Contains(p.description, "\n") {
					hasMultiLineDesc = true
				}
			}
		}

		if !hasDesc {
			continue
		}

		// Compute canonical key for shared type detection.
		canonicalKey := canonicalReturnTypeKey(props)

		parts := strings.Split(tool.Name, ".")
		method := parts[len(parts)-1]

		if hasMultiLineDesc {
			// Named type mode.
			if existing, ok := sharedReturnTypes[canonicalKey]; ok {
				// Shared with a previously seen tool - reuse the name.
				returnTypes[tool.Name] = returnTypeInfo{mode: "named", typeName: existing.typeName}
			} else {
				typeName := upperFirst(method) + "Result"
				sharedReturnTypes[canonicalKey] = &sharedInfo{typeName: typeName, properties: props}
				returnTypes[tool.Name] = returnTypeInfo{mode: "named", typeName: typeName}
			}
		} else {
			// Inline with single-line comments.
			if existing, ok := sharedReturnTypes[canonicalKey]; ok {
				returnTypes[tool.Name] = returnTypeInfo{mode: "named", typeName: existing.typeName}
			} else {
				// Check if another tool shares this exact structure.
				// We'll store it for dedup but render inline unless shared.
				sharedReturnTypes[canonicalKey] = &sharedInfo{
					typeName:   upperFirst(method) + "Result",
					properties: props,
				}
				returnTypes[tool.Name] = returnTypeInfo{mode: "inline-comments"}
			}
		}
	}

	// Count usage of each canonical key to detect shared types.
	canonicalUsage := map[string]int{}
	for _, tool := range tools {
		if tool.Sig == nil {
			continue
		}
		rt := tool.Sig.Return()
		if rt == nil {
			continue
		}
		unwrapped := rt.UnwrapPromise()
		if !unwrapped.IsObject() {
			continue
		}
		props := returnTypeProperties(unwrapped)
		if len(props) == 0 {
			continue
		}
		key := canonicalReturnTypeKey(props)
		canonicalUsage[key]++
	}

	// Promote inline-comments to named if used by multiple tools.
	for toolName, info := range returnTypes {
		if info.mode != "inline-comments" {
			continue
		}
		tool := findTool(tools, toolName)
		if tool == nil {
			continue
		}
		rt := tool.Sig.Return()
		if rt == nil {
			continue
		}
		unwrapped := rt.UnwrapPromise()
		props := returnTypeProperties(unwrapped)
		key := canonicalReturnTypeKey(props)
		if canonicalUsage[key] > 1 {
			shared := sharedReturnTypes[key]
			returnTypes[toolName] = returnTypeInfo{mode: "named", typeName: shared.typeName}
		}
	}

	// Emit named return type interfaces.
	emittedInterfaces := map[string]bool{}
	for _, tool := range tools {
		info, ok := returnTypes[tool.Name]
		if !ok || info.mode != "named" || emittedInterfaces[info.typeName] {
			continue
		}
		emittedInterfaces[info.typeName] = true

		unwrapped := tool.Sig.Return().UnwrapPromise()
		props := returnTypeProperties(unwrapped)

		fmt.Fprintf(&b, "interface %s {\n", info.typeName)
		schema := unwrapped.ToJSONSchema()
		reqSet := jsonSchemaRequiredSet(schema)
		for _, p := range props {
			if p.description != "" {
				if strings.Contains(p.description, "\n") {
					b.WriteString("  /**\n")
					for _, line := range strings.Split(p.description, "\n") {
						fmt.Fprintf(&b, "   * %s\n", line)
					}
					b.WriteString("   */\n")
				} else {
					fmt.Fprintf(&b, "  /** %s */\n", p.description)
				}
			}
			optional := ""
			if !reqSet[p.name] {
				optional = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", p.name, optional, p.tsType)
		}
		b.WriteString("}\n\n")
	}

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
					unwrapped := rt.UnwrapPromise()
					if info, ok := returnTypes[tool.Name]; ok {
						switch info.mode {
						case "named":
							returnType = info.typeName
						case "inline-comments":
							returnType = renderReturnTypeInlineComments(unwrapped)
						default:
							returnType = unwrapped.ToTS()
						}
					} else {
						returnType = unwrapped.ToTS()
					}
				}
			}

			fmt.Fprintf(&b, "    %s(%s): %s;\n", method, strings.Join(paramParts, ", "), returnType)
		}
		b.WriteString("  };\n")
	}
	b.WriteString("};\n")

	return b.String()
}

// returnPropInfo holds property-level information extracted from a return type.
type returnPropInfo struct {
	name        string
	tsType      string
	description string
	required    bool
}

// returnTypeProperties extracts property information from an object ParamsType
// by combining PropertyNames() with ToJSONSchema() for descriptions.
func returnTypeProperties(pt *toolbox.ParamsType) []returnPropInfo {
	if pt == nil || !pt.IsObject() {
		return nil
	}
	names := pt.PropertyNames()
	if len(names) == 0 {
		return nil
	}

	schema := pt.ToJSONSchema()
	reqSet := jsonSchemaRequiredSet(schema)

	// Get the properties map from the JSON schema.
	propsMap, _ := schema["properties"].(map[string]any)

	var result []returnPropInfo
	for _, name := range names {
		var desc string
		if propsMap != nil {
			if propSchema, ok := propsMap[name].(map[string]any); ok {
				desc, _ = propSchema["description"].(string)
			}
		}

		// Get the TS type for this property. We use the full type's ToTS() and
		// extract per-property types by removing other properties.
		propType := pt.RemoveProperties(removeAllExcept(names, name)...).ToTS()
		// The result of ToTS() for a single-property object is like "{ name?: type }".
		// We need to extract just the type part.
		propType = extractSinglePropertyType(propType, name)

		result = append(result, returnPropInfo{
			name:        name,
			tsType:      propType,
			description: desc,
			required:    reqSet[name],
		})
	}
	return result
}

// removeAllExcept returns all names except the given one.
func removeAllExcept(names []string, keep string) []string {
	var result []string
	for _, n := range names {
		if n != keep {
			result = append(result, n)
		}
	}
	return result
}

// extractSinglePropertyType extracts the type from a single-property object
// type string like "{ name?: type }" or "{ name: type }".
func extractSinglePropertyType(objectTS string, propName string) string {
	// Look for "name?: " or "name: " pattern.
	for _, pattern := range []string{propName + "?: ", propName + ": "} {
		idx := strings.Index(objectTS, pattern)
		if idx >= 0 {
			rest := objectTS[idx+len(pattern):]
			// Remove trailing " }" or "}"
			rest = strings.TrimSuffix(rest, " }")
			rest = strings.TrimSuffix(rest, "}")
			return rest
		}
	}
	return objectTS
}

// jsonSchemaRequiredSet returns the set of required property names from a JSON schema.
func jsonSchemaRequiredSet(schema map[string]any) map[string]bool {
	reqSet := map[string]bool{}
	if reqArr, ok := schema["required"].([]any); ok {
		for _, r := range reqArr {
			if s, ok := r.(string); ok {
				reqSet[s] = true
			}
		}
	}
	return reqSet
}

// canonicalReturnTypeKey produces a deterministic string key for a return type
// structure, used to detect shared types across tools.
func canonicalReturnTypeKey(props []returnPropInfo) string {
	type propKey struct {
		Name        string `json:"n"`
		Type        string `json:"t"`
		Description string `json:"d,omitempty"`
		Required    bool   `json:"r,omitempty"`
	}
	keys := make([]propKey, len(props))
	for i, p := range props {
		keys[i] = propKey{Name: p.name, Type: p.tsType, Description: p.description, Required: p.required}
	}
	data, _ := json.Marshal(keys)
	return string(data)
}

// renderReturnTypeInlineComments renders an object return type with inline
// /** desc */ comments for single-line property descriptions.
func renderReturnTypeInlineComments(pt *toolbox.ParamsType) string {
	props := returnTypeProperties(pt)
	if len(props) == 0 {
		return pt.ToTS()
	}

	schema := pt.ToJSONSchema()
	reqSet := jsonSchemaRequiredSet(schema)

	var parts []string
	for _, p := range props {
		optional := ""
		if !reqSet[p.name] {
			optional = "?"
		}
		if p.description != "" {
			parts = append(parts, fmt.Sprintf("/** %s */ %s%s: %s", p.description, p.name, optional, p.tsType))
		} else {
			parts = append(parts, fmt.Sprintf("%s%s: %s", p.name, optional, p.tsType))
		}
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

// upperFirst returns s with the first letter uppercased.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// findTool returns the tool with the given name, or nil.
func findTool(tools []toolset.AgentTool, name string) *toolset.AgentTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
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

// collectUniqueDeclarations gathers type alias declarations from all tools'
// param and return types, deduplicating by exact line content. This ensures
// that when multiple tools reference the same named type (e.g. TicketFields),
// it is emitted exactly once. Lines are returned in sorted order for
// deterministic output.
func collectUniqueDeclarations(tools []toolset.AgentTool) []string {
	seen := map[string]bool{}
	var lines []string
	for _, tool := range tools {
		sources := []*toolbox.ParamsType{}
		if pt := tool.ParamsType(); pt != nil {
			sources = append(sources, pt)
		}
		if tool.Sig != nil {
			if rt := tool.Sig.Return(); rt != nil {
				sources = append(sources, rt)
			}
		}
		for _, src := range sources {
			if decls := src.Declarations(); decls != "" {
				for _, line := range strings.Split(strings.TrimRight(decls, "\n"), "\n") {
					if line != "" && !seen[line] {
						seen[line] = true
						lines = append(lines, line)
					}
				}
			}
		}
	}
	sort.Strings(lines)
	return lines
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
