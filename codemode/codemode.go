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

	// Collect type declarations (emitted after the tools block).
	declLines := collectUniqueDeclarations(tools)

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

	// Build a map from canonical type structure to $ref definition name.
	// This lets us reuse the original source type name (e.g. "Ticket")
	// for shared return types instead of generating "CreateResult".
	defNameByStructure := map[string]string{}
	for _, tool := range tools {
		if pt := tool.ParamsType(); pt != nil {
			collectDefinitionStructures(pt, defNameByStructure)
		}
	}

	// Detect shared parameter types across tools. When the same parameter
	// type appears in multiple tools (by structural equality), it gets
	// extracted as a named type at the top of the file.
	type sharedParamInfo struct {
		typeName string
		tsType   string // ToTS() rendering
	}
	sharedParamTypes := map[string]*sharedParamInfo{} // canonical key -> info
	// Map from (tool.Name, paramName) -> shared type name.
	paramTypeOverrides := map[string]string{}

	// First pass: count usages of each param type structure.
	type paramOccurrence struct {
		toolName  string
		paramName string
		tsType    string
	}
	paramCanonical := map[string][]paramOccurrence{}
	for _, tool := range tools {
		if tool.Sig == nil {
			continue
		}
		for _, p := range tool.Sig.Params() {
			pt := p.Type()
			if pt == nil || !pt.IsObject() {
				continue
			}
			props := returnTypeProperties(pt)
			if len(props) == 0 {
				continue
			}
			key := canonicalReturnTypeKey(props)
			paramCanonical[key] = append(paramCanonical[key], paramOccurrence{
				toolName:  tool.Name,
				paramName: p.Name(),
				tsType:    pt.ToTS(),
			})
		}
	}

	// Second pass: for param types used by 2+ tools, create named types.
	for key, occs := range paramCanonical {
		if len(occs) < 2 {
			continue
		}
		// Check if a $ref definition name already exists for this structure.
		typeName := defNameByStructure[key]
		if typeName == "" {
			// Derive a name from the param name of the first occurrence.
			typeName = upperFirst(occs[0].paramName)
		}
		sharedParamTypes[key] = &sharedParamInfo{
			typeName: typeName,
			tsType:   occs[0].tsType,
		}
		for _, occ := range occs {
			paramTypeOverrides[occ.toolName+"."+occ.paramName] = typeName
		}
	}

	// Collect shared param types for emission after the tools block.
	declLineSet := map[string]bool{}
	for _, line := range declLines {
		declLineSet[line] = true
	}
	for _, info := range sharedParamTypes {
		declLine := fmt.Sprintf("type %s = %s;", info.typeName, info.tsType)
		if !declLineSet[declLine] {
			declLines = append(declLines, declLine)
		}
	}

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

		// Prefer the $ref definition name if one exists with this structure.
		deriveName := func(fallback string) string {
			if defName, ok := defNameByStructure[canonicalKey]; ok {
				return defName
			}
			return fallback
		}

		if hasMultiLineDesc {
			// Named type mode.
			if existing, ok := sharedReturnTypes[canonicalKey]; ok {
				// Shared with a previously seen tool - reuse the name.
				returnTypes[tool.Name] = returnTypeInfo{mode: "named", typeName: existing.typeName}
			} else {
				typeName := deriveName(upperFirst(method) + "Result")
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
					typeName:   deriveName(upperFirst(method) + "Result"),
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

	// Collect the set of named return type names that will be emitted as
	// interface blocks, so we can suppress matching $ref type aliases.
	namedReturnTypeNames := map[string]bool{}
	for _, info := range returnTypes {
		if info.mode == "named" {
			namedReturnTypeNames[info.typeName] = true
		}
	}

	// Emit named return type interfaces, skipping those that already exist
	// as type aliases from $ref declarations.
	emittedInterfaces := map[string]bool{}
	for _, tool := range tools {
		info, ok := returnTypes[tool.Name]
		if !ok || info.mode != "named" || emittedInterfaces[info.typeName] {
			continue
		}
		emittedInterfaces[info.typeName] = true

		// If a $ref type alias with the same name was already emitted in
		// declarations, skip the interface to avoid duplicate definitions.
		typeAliasPrefix := "type " + info.typeName + " = "
		alreadyDeclared := false
		for _, line := range declLines {
			if strings.HasPrefix(line, typeAliasPrefix) {
				alreadyDeclared = true
				break
			}
		}
		if alreadyDeclared {
			continue
		}

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
					// then check for shared param type override,
					// otherwise use the param's TypeScript type.
					var tsType string
					if litVal, ok := literals[p.Name()]; ok {
						tsType = literalToTS(litVal)
					} else if sharedName, ok := paramTypeOverrides[tool.Name+"."+p.Name()]; ok {
						tsType = sharedName
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

	// Emit type declarations after the tools block so the reader sees
	// the tool surface first and supporting types below.
	if len(declLines) > 0 {
		b.WriteString("\n")
		for _, line := range declLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

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

// collectDefinitionStructures builds a map from canonical type structure key
// to the $ref definition name. This allows matching a shared return type's
// structure to the original source type name (e.g. "Ticket").
func collectDefinitionStructures(pt *toolbox.ParamsType, out map[string]string) {
	if pt == nil {
		return
	}
	decls := pt.Declarations()
	if decls == "" {
		return
	}
	// Each declaration is "type Foo = <body>;\n".
	// We need to parse out the name and match it to a structural key.
	// The definitions are available via the ParamsType's ToJSONSchema under "definitions".
	schema := pt.ToJSONSchema()
	defsRaw, ok := schema["definitions"]
	if !ok {
		return
	}
	defs, ok := defsRaw.(map[string]any)
	if !ok {
		return
	}
	for name, defRaw := range defs {
		defMap, ok := defRaw.(map[string]any)
		if !ok {
			continue
		}
		// Build returnPropInfo from the definition's properties.
		propsRaw, ok := defMap["properties"].(map[string]any)
		if !ok {
			continue
		}
		reqSet := map[string]bool{}
		if reqArr, ok := defMap["required"].([]any); ok {
			for _, r := range reqArr {
				if s, ok := r.(string); ok {
					reqSet[s] = true
				}
			}
		}
		// Sort property names for deterministic ordering.
		var propNames []string
		for pn := range propsRaw {
			propNames = append(propNames, pn)
		}
		sort.Strings(propNames)

		var props []returnPropInfo
		for _, pn := range propNames {
			propSchema, ok := propsRaw[pn].(map[string]any)
			if !ok {
				continue
			}
			desc, _ := propSchema["description"].(string)
			// Get TypeScript type from the JSON Schema property.
			tsType := jsonSchemaPropertyToTS(propSchema)
			props = append(props, returnPropInfo{
				name:        pn,
				tsType:      tsType,
				description: desc,
				required:    reqSet[pn],
			})
		}
		if len(props) > 0 {
			key := canonicalReturnTypeKey(props)
			if _, exists := out[key]; !exists {
				out[key] = name
			}
		}
	}
}

// jsonSchemaPropertyToTS converts a JSON Schema property to a TypeScript type string.
// This is a simplified version for matching purposes.
func jsonSchemaPropertyToTS(schema map[string]any) string {
	// Handle enum values.
	if enumVals, ok := schema["enum"].([]any); ok {
		parts := make([]string, len(enumVals))
		for i, v := range enumVals {
			switch val := v.(type) {
			case string:
				parts[i] = fmt.Sprintf("%q", val)
			case float64:
				if val == float64(int64(val)) {
					parts[i] = fmt.Sprintf("%d", int64(val))
				} else {
					parts[i] = fmt.Sprintf("%g", val)
				}
			case bool:
				if val {
					parts[i] = "true"
				} else {
					parts[i] = "false"
				}
			default:
				parts[i] = fmt.Sprintf("%v", val)
			}
		}
		sort.Strings(parts)
		return strings.Join(parts, " | ")
	}
	// Handle type field.
	if t, ok := schema["type"].(string); ok {
		switch t {
		case "string":
			return "string"
		case "number", "integer":
			return "number"
		case "boolean":
			return "boolean"
		case "array":
			if items, ok := schema["items"].(map[string]any); ok {
				return jsonSchemaPropertyToTS(items) + "[]"
			}
			return "any[]"
		case "object":
			return "Record<string, any>"
		}
	}
	return "any"
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
//
// When a definition has properties with descriptions (from the JSON Schema),
// the declaration is re-rendered with inline /** desc */ comments on each
// described property. Multi-line descriptions cause the declaration to be
// rendered as a named interface with JSDoc blocks.
func collectUniqueDeclarations(tools []toolset.AgentTool) []string {
	// First pass: collect all definition schemas keyed by name from all tools'
	// JSON schemas. These contain the property-level descriptions.
	defSchemas := map[string]map[string]any{}
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
			schema := src.ToJSONSchema()
			defsRaw, ok := schema["definitions"]
			if !ok {
				continue
			}
			defs, ok := defsRaw.(map[string]any)
			if !ok {
				continue
			}
			for name, defRaw := range defs {
				defMap, ok := defRaw.(map[string]any)
				if !ok {
					continue
				}
				if _, exists := defSchemas[name]; !exists {
					defSchemas[name] = defMap
				}
			}
		}
	}

	// Second pass: collect declaration lines, enhancing them with descriptions.
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
					if line == "" || seen[line] {
						continue
					}
					// Try to enhance the declaration with property descriptions.
					enhanced := enhanceDeclWithDescriptions(line, defSchemas)
					if !seen[enhanced] {
						seen[line] = true
						seen[enhanced] = true
						lines = append(lines, enhanced)
					}
				}
			}
		}
	}
	sort.Strings(lines)
	return lines
}

// enhanceDeclWithDescriptions takes a declaration line like
// "type Foo = { bar?: string; baz?: number };" and, if a matching definition
// schema with property descriptions exists, re-renders it with inline
// /** desc */ comments. If any description is multi-line, the result is
// rendered as an interface with JSDoc blocks instead.
//
// Property TS types are extracted from the original declaration to preserve
// full fidelity (e.g. nested object types) rather than being regenerated
// from the simplified JSON schema.
func enhanceDeclWithDescriptions(line string, defSchemas map[string]map[string]any) string {
	// Parse "type <Name> = <body>;" to extract the name.
	if !strings.HasPrefix(line, "type ") {
		return line
	}
	rest := line[len("type "):]
	eqIdx := strings.Index(rest, " = ")
	if eqIdx < 0 {
		return line
	}
	typeName := rest[:eqIdx]

	defSchema, ok := defSchemas[typeName]
	if !ok {
		return line
	}
	propsRaw, ok := defSchema["properties"].(map[string]any)
	if !ok {
		return line
	}

	// Build a description map from the JSON schema.
	descMap := map[string]string{}
	hasDesc := false
	hasMultiLineDesc := false
	for pn, propRaw := range propsRaw {
		propMap, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		desc, _ := propMap["description"].(string)
		if desc != "" {
			descMap[pn] = desc
			hasDesc = true
			if strings.Contains(desc, "\n") {
				hasMultiLineDesc = true
			}
		}
	}
	if !hasDesc {
		return line
	}

	// Extract the body between "type Foo = " and the trailing ";".
	body := rest[eqIdx+3:]           // after " = "
	body = strings.TrimSuffix(body, ";") // remove trailing ";"
	body = strings.TrimSpace(body)

	// Parse the body to extract property entries with their original TS types.
	parsedProps := parseDeclBody(body)
	if len(parsedProps) == 0 {
		return line
	}

	if hasMultiLineDesc {
		// Render as an interface with JSDoc blocks.
		var b strings.Builder
		fmt.Fprintf(&b, "interface %s {\n", typeName)
		for _, p := range parsedProps {
			if desc := descMap[p.name]; desc != "" {
				if strings.Contains(desc, "\n") {
					b.WriteString("  /**\n")
					for _, dl := range strings.Split(desc, "\n") {
						fmt.Fprintf(&b, "   * %s\n", dl)
					}
					b.WriteString("   */\n")
				} else {
					fmt.Fprintf(&b, "  /** %s */\n", desc)
				}
			}
			fmt.Fprintf(&b, "  %s: %s;\n", p.nameWithOpt, p.tsType)
		}
		b.WriteString("}")
		return b.String()
	}

	// Render as inline type alias with /** desc */ comments.
	var parts []string
	for _, p := range parsedProps {
		if desc := descMap[p.name]; desc != "" {
			parts = append(parts, fmt.Sprintf("/** %s */ %s: %s", desc, p.nameWithOpt, p.tsType))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", p.nameWithOpt, p.tsType))
		}
	}
	return fmt.Sprintf("type %s = { %s };", typeName, strings.Join(parts, "; "))
}

// declProp represents a parsed property from a type alias body.
type declProp struct {
	name        string // bare property name (e.g. "foo")
	nameWithOpt string // name with optional marker (e.g. "foo?")
	tsType      string // original TS type string
}

// parseDeclBody parses a type alias body like "{ foo?: string; bar?: number }"
// into individual property entries, correctly handling nested braces.
func parseDeclBody(body string) []declProp {
	// Strip outer braces.
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return nil
	}
	inner := strings.TrimSpace(body[1 : len(body)-1])
	if inner == "" {
		return nil
	}

	// Split on "; " at brace depth 0.
	var entries []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ';':
			if depth == 0 {
				entry := strings.TrimSpace(inner[start:i])
				if entry != "" {
					entries = append(entries, entry)
				}
				start = i + 1
			}
		}
	}
	// Remaining after last semicolon.
	if tail := strings.TrimSpace(inner[start:]); tail != "" {
		entries = append(entries, tail)
	}

	var props []declProp
	for _, entry := range entries {
		// Each entry is like "foo?: type" or "foo: type".
		colonIdx := strings.Index(entry, ": ")
		if colonIdx < 0 {
			continue
		}
		nameWithOpt := entry[:colonIdx]
		tsType := entry[colonIdx+2:]
		name := strings.TrimSuffix(nameWithOpt, "?")
		props = append(props, declProp{
			name:        name,
			nameWithOpt: nameWithOpt,
			tsType:      tsType,
		})
	}
	return props
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
