package codemode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/toolbox"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

// DeclarationSource generates a .d.ts file with JSDoc comments for the
// agent-visible tools. This is the type declaration the agent imports.
func DeclarationSource(resolved toolset.ResolvedToolset) string {
	var b strings.Builder

	view := resolved.AgentView()
	tools := sortedTools(view)

	// Collect type declarations (emitted after the tools block).
	declLines := collectUniqueDeclarations(tools)

	namespaces, nsNames := groupToolsByNamespace(tools)

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
			props := pt.ObjectProperties()
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
		properties []toolbox.PropertyInfo
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
		props := unwrapped.ObjectProperties()
		if len(props) == 0 {
			continue
		}

		// Check if any property has a description.
		hasDesc := false
		hasMultiLineDesc := false
		for _, p := range props {
			if p.Description != "" {
				hasDesc = true
				if strings.Contains(p.Description, "\n") {
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
		cProps := unwrapped.ObjectProperties()
		if len(cProps) == 0 {
			continue
		}
		key := canonicalReturnTypeKey(cProps)
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
		pProps := unwrapped.ObjectProperties()
		key := canonicalReturnTypeKey(pProps)
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
		props := unwrapped.ObjectProperties()

		fmt.Fprintf(&b, "interface %s {\n", info.typeName)
		for _, p := range props {
			if p.Description != "" {
				if strings.Contains(p.Description, "\n") {
					b.WriteString("  /**\n")
					for _, line := range strings.Split(p.Description, "\n") {
						fmt.Fprintf(&b, "   * %s\n", line)
					}
					b.WriteString("   */\n")
				} else {
					fmt.Fprintf(&b, "  /** %s */\n", p.Description)
				}
			}
			optional := ""
			if p.Optional {
				optional = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", p.Name, optional, p.Type.ToTS())
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

// upperFirst returns s with the first letter uppercased.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// renderReturnTypeInlineComments renders an object return type with inline
// /** desc */ comments for single-line property descriptions.
func renderReturnTypeInlineComments(pt *toolbox.TSType) string {
	props := pt.ObjectProperties()
	if len(props) == 0 {
		return pt.ToTS()
	}

	var parts []string
	for _, p := range props {
		optional := ""
		if p.Optional {
			optional = "?"
		}
		if p.Description != "" {
			parts = append(parts, fmt.Sprintf("/** %s */ %s%s: %s", p.Description, p.Name, optional, p.Type.ToTS()))
		} else {
			parts = append(parts, fmt.Sprintf("%s%s: %s", p.Name, optional, p.Type.ToTS()))
		}
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

// canonicalReturnTypeKey produces a deterministic string key for a return type
// structure, used to detect shared types across tools.
func canonicalReturnTypeKey(props []toolbox.PropertyInfo) string {
	type propKey struct {
		Name        string `json:"n"`
		Type        string `json:"t"`
		Description string `json:"d,omitempty"`
		Required    bool   `json:"r,omitempty"`
	}
	keys := make([]propKey, len(props))
	for i, p := range props {
		keys[i] = propKey{Name: p.Name, Type: p.Type.ToTS(), Description: p.Description, Required: !p.Optional}
	}
	data, _ := json.Marshal(keys)
	return string(data)
}

// collectUniqueDeclarations gathers type alias declarations from all tools'
// param and return types, deduplicating by exact line content. This ensures
// that when multiple tools reference the same named type (e.g. TicketFields),
// it is emitted exactly once. Lines are returned in sorted order for
// deterministic output.
//
// When a definition has properties with descriptions (from Properties()),
// the declaration is re-rendered with inline /** desc */ comments on each
// described property. Multi-line descriptions cause the declaration to be
// rendered as a named interface with JSDoc blocks.
func collectUniqueDeclarations(tools []toolset.AgentTool) []string {
	// First pass: collect all definition types keyed by name from all tools'
	// param and return types. These contain the property-level descriptions.
	defTypes := map[string]*toolbox.TSType{}
	for _, tool := range tools {
		sources := []*toolbox.TSType{}
		if pt := tool.ParamsType(); pt != nil {
			sources = append(sources, pt)
		}
		if tool.Sig != nil {
			if rt := tool.Sig.Return(); rt != nil {
				sources = append(sources, rt)
			}
		}
		for _, src := range sources {
			for name, dt := range src.DefinitionTypes() {
				if _, exists := defTypes[name]; !exists {
					defTypes[name] = dt
				}
			}
		}
	}

	// Second pass: collect declaration lines, enhancing them with descriptions.
	seen := map[string]bool{}
	var lines []string
	for _, tool := range tools {
		sources := []*toolbox.TSType{}
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
					enhanced := enhanceDeclWithDescriptions(line, defTypes)
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
// type with property descriptions exists, re-renders it with inline
// /** desc */ comments. If any description is multi-line, the result is
// rendered as an interface with JSDoc blocks instead.
//
// Property TS types come from Properties() on the definition type, preserving
// full fidelity (e.g. nested object types).
func enhanceDeclWithDescriptions(line string, defTypes map[string]*toolbox.TSType) string {
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

	defType, ok := defTypes[typeName]
	if !ok {
		return line
	}
	props := defType.ObjectProperties()
	if len(props) == 0 {
		return line
	}

	// Check if any property has a description.
	hasDesc := false
	hasMultiLineDesc := false
	for _, p := range props {
		if p.Description != "" {
			hasDesc = true
			if strings.Contains(p.Description, "\n") {
				hasMultiLineDesc = true
			}
		}
	}
	if !hasDesc {
		return line
	}

	if hasMultiLineDesc {
		// Render as an interface with JSDoc blocks.
		var b strings.Builder
		fmt.Fprintf(&b, "interface %s {\n", typeName)
		for _, p := range props {
			if p.Description != "" {
				if strings.Contains(p.Description, "\n") {
					b.WriteString("  /**\n")
					for _, dl := range strings.Split(p.Description, "\n") {
						fmt.Fprintf(&b, "   * %s\n", dl)
					}
					b.WriteString("   */\n")
				} else {
					fmt.Fprintf(&b, "  /** %s */\n", p.Description)
				}
			}
			nameWithOpt := p.Name
			if p.Optional {
				nameWithOpt += "?"
			}
			fmt.Fprintf(&b, "  %s: %s;\n", nameWithOpt, p.Type.ToTS())
		}
		b.WriteString("}")
		return b.String()
	}

	// Render as inline type alias with /** desc */ comments.
	var parts []string
	for _, p := range props {
		nameWithOpt := p.Name
		if p.Optional {
			nameWithOpt += "?"
		}
		if p.Description != "" {
			parts = append(parts, fmt.Sprintf("/** %s */ %s: %s", p.Description, nameWithOpt, p.Type.ToTS()))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", nameWithOpt, p.Type.ToTS()))
		}
	}
	return fmt.Sprintf("type %s = { %s };", typeName, strings.Join(parts, "; "))
}

// collectDefinitionStructures builds a map from canonical type structure key
// to the $ref definition name. This allows matching a shared return type's
// structure to the original source type name (e.g. "Ticket").
func collectDefinitionStructures(pt *toolbox.TSType, out map[string]string) {
	if pt == nil {
		return
	}
	defTypes := pt.DefinitionTypes()
	for name, defType := range defTypes {
		props := defType.ObjectProperties()
		if len(props) == 0 {
			continue
		}
		key := canonicalReturnTypeKey(props)
		if _, exists := out[key]; !exists {
			out[key] = name
		}
	}
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
