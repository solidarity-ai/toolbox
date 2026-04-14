package codemodesdks

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

// sortedTools returns the tools from the view sorted by name.
func sortedTools(view toolset.AgentView) []toolset.AgentTool {
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

// DeclarationSource generates a .d.ts file with JSDoc comments for the
// agent-visible tools as concat-safe ambient package namespaces.
func DeclarationSource(prepared toolset.PreparedToolset) string {
	var b strings.Builder

	groups := groupedPackageNames(prepared)
	for i, packageName := range groups {
		if i > 0 {
			b.WriteString("\n\n")
		}
		pkgPrepared := prepared.FilterTools(func(tool toolset.PreparedTool) bool {
			return preparedToolPackageName(tool) == packageName
		})
		b.WriteString(packageDeclarationSource(packageName, sortedTools(pkgPrepared.AgentView())))
	}

	return strings.TrimRight(b.String(), "\n")
}

type returnTypeInfo struct {
	mode     string // "named", "inline-comments", "plain"
	typeName string // for "named" mode
}

type returnCandidate struct {
	method           string
	shape            *toolbox.TSType
	properties       []toolbox.PropertyInfo
	refName          string
	hasDescriptions  bool
	hasMultiLineDesc bool
}

type declNamespaceNode struct {
	funcs    []toolset.AgentTool
	children map[string]*declNamespaceNode
}

func groupedPackageNames(prepared toolset.PreparedToolset) []string {
	seen := map[string]bool{}
	var names []string
	for _, tool := range prepared.Tools() {
		name := preparedToolPackageName(tool)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func preparedToolPackageName(tool toolset.PreparedTool) string {
	name := strings.TrimSpace(tool.Name)
	if tool.PackageMeta != nil {
		if pkgName := strings.TrimSpace(tool.PackageMeta.Name); pkgName != "" {
			return pkgName
		}
		if module := strings.TrimSpace(tool.PackageMeta.Module.String()); module != "" {
			return module
		}
	}
	if name == "" {
		return "pkg"
	}
	return sanitizeIdentifierSegment(name)
}

func packageDeclarationSource(packageName string, tools []toolset.AgentTool) string {
	if len(tools) == 0 {
		return ""
	}

	// Build a map from canonical type structure to $ref definition name.
	defNameByStructure := map[string]string{}
	for _, tool := range tools {
		if pt := tool.ParamsType(); pt != nil {
			collectDefinitionStructures(pt, defNameByStructure)
		}
	}

	type sharedParamInfo struct {
		typeName string
		tsType   string
	}
	sharedParamTypes := map[string]*sharedParamInfo{}
	paramTypeOverrides := map[string]string{}

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
	for key, occs := range paramCanonical {
		if len(occs) < 2 {
			continue
		}
		typeName := defNameByStructure[key]
		if typeName == "" {
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

	returnTypes := map[string]returnTypeInfo{}
	returnCandidates := map[string]returnCandidate{}
	canonicalUsage := map[string]int{}
	canonicalRefNames := map[string]map[string]int{}
	canonicalFallbackNames := map[string]string{}
	for _, tool := range tools {
		if tool.Sig == nil {
			continue
		}
		rt := tool.Sig.Return()
		if rt == nil {
			continue
		}
		shape, refName := sdkReturnShape(rt)
		if shape == nil || !shape.IsObject() {
			continue
		}
		props := shape.ObjectProperties()
		if len(props) == 0 {
			continue
		}

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
		parts := strings.Split(tool.Name, ".")
		method := parts[len(parts)-1]

		canonicalKey := canonicalReturnTypeKey(props)
		canonicalUsage[canonicalKey]++
		if refName != "" {
			if canonicalRefNames[canonicalKey] == nil {
				canonicalRefNames[canonicalKey] = map[string]int{}
			}
			canonicalRefNames[canonicalKey][refName]++
		}
		if _, ok := canonicalFallbackNames[canonicalKey]; !ok {
			canonicalFallbackNames[canonicalKey] = upperFirst(method) + "Result"
		}

		returnCandidates[tool.Name] = returnCandidate{
			method:           method,
			shape:            shape,
			properties:       props,
			refName:          refName,
			hasDescriptions:  hasDesc,
			hasMultiLineDesc: hasMultiLineDesc,
		}
	}

	for toolName, candidate := range returnCandidates {
		key := canonicalReturnTypeKey(candidate.properties)
		if canonicalUsage[key] > 1 {
			returnTypes[toolName] = returnTypeInfo{
				mode:     "named",
				typeName: preferredSharedReturnTypeName(key, defNameByStructure, canonicalRefNames, canonicalFallbackNames),
			}
			continue
		}
		if !candidate.hasDescriptions {
			continue
		}
		if candidate.hasMultiLineDesc {
			returnTypes[toolName] = returnTypeInfo{
				mode:     "named",
				typeName: canonicalFallbackNames[key],
			}
			continue
		}
		returnTypes[toolName] = returnTypeInfo{mode: "inline-comments"}
	}

	refDeclLines := collectUniqueDeclarations(tools, returnTypes)
	var inputDeclLines []string
	declLineSet := map[string]bool{}
	for _, info := range sharedParamTypes {
		declLine := fmt.Sprintf("type %s = %s;", info.typeName, info.tsType)
		if !declLineSet[declLine] {
			inputDeclLines = append(inputDeclLines, declLine)
			declLineSet[declLine] = true
		}
	}
	var refLines []string
	for _, line := range refDeclLines {
		if !declLineSet[line] {
			refLines = append(refLines, line)
			declLineSet[line] = true
		}
	}

	var interfaceBlocks []string
	emittedInterfaces := map[string]bool{}
	for _, tool := range tools {
		info, ok := returnTypes[tool.Name]
		if !ok || info.mode != "named" || emittedInterfaces[info.typeName] {
			continue
		}
		emittedInterfaces[info.typeName] = true

		typeAliasLine := "type " + info.typeName + " = "
		alreadyDeclared := false
		for line := range declLineSet {
			if strings.Contains(line, typeAliasLine) {
				alreadyDeclared = true
				break
			}
		}
		if alreadyDeclared {
			continue
		}

		candidate, ok := returnCandidates[tool.Name]
		if !ok || candidate.shape == nil {
			continue
		}
		props := candidate.properties
		var ib strings.Builder
		if desc := candidate.shape.Description(); desc != "" {
			fmt.Fprintf(&ib, "// %s\n", desc)
		}
		fmt.Fprintf(&ib, "interface %s {\n", info.typeName)
		for _, p := range props {
			if p.Description != "" {
				for _, line := range strings.Split(p.Description, "\n") {
					fmt.Fprintf(&ib, "  // %s\n", line)
				}
			}
			optional := ""
			if p.Optional {
				optional = "?"
			}
			fmt.Fprintf(&ib, "  %s%s: %s;\n", p.Name, optional, p.Type.ToTS())
		}
		ib.WriteString("}")
		interfaceBlocks = append(interfaceBlocks, ib.String())
	}

	tree := buildNamespaceTree(tools)

	var body strings.Builder
	renderNamespaceNode(&body, "", tree, returnTypes, paramTypeOverrides)
	hasTypes := len(inputDeclLines) > 0 || len(refLines) > 0 || len(interfaceBlocks) > 0
	if hasTypes {
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		for _, line := range inputDeclLines {
			body.WriteString(line)
			body.WriteString("\n")
		}
		for _, line := range refLines {
			body.WriteString(line)
			body.WriteString("\n")
		}
		for _, block := range interfaceBlocks {
			body.WriteString(block)
			body.WriteString("\n")
		}
	}

	return wrapNamespacePath(namespaceSegments(packageName), strings.TrimRight(body.String(), "\n"))
}

func buildNamespaceTree(tools []toolset.AgentTool) *declNamespaceNode {
	root := &declNamespaceNode{}
	for _, tool := range tools {
		segments := toolSegments(tool.Name)
		if len(segments) == 0 {
			continue
		}
		node := root
		for _, segment := range segments[:len(segments)-1] {
			if node.children == nil {
				node.children = map[string]*declNamespaceNode{}
			}
			child := node.children[segment]
			if child == nil {
				child = &declNamespaceNode{}
				node.children[segment] = child
			}
			node = child
		}
		node.funcs = append(node.funcs, tool)
	}
	return root
}

func renderNamespaceNode(b *strings.Builder, indent string, node *declNamespaceNode, returnTypes map[string]returnTypeInfo, paramTypeOverrides map[string]string) {
	if node == nil {
		return
	}
	for _, tool := range node.funcs {
		method := sanitizeIdentifierSegment(lastSegment(tool.Name))
		writeToolDeclaration(b, indent, method, tool, returnTypes, paramTypeOverrides)
	}
	if len(node.funcs) > 0 && len(node.children) > 0 {
		b.WriteString("\n")
	}
	childNames := make([]string, 0, len(node.children))
	for name := range node.children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for i, name := range childNames {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "%snamespace %s {\n", indent, sanitizeIdentifierSegment(name))
		renderNamespaceNode(b, indent+"  ", node.children[name], returnTypes, paramTypeOverrides)
		fmt.Fprintf(b, "%s}\n", indent)
	}
}

func writeToolDeclaration(b *strings.Builder, indent, method string, tool toolset.AgentTool, returnTypes map[string]returnTypeInfo, paramTypeOverrides map[string]string) {
	hidden := tool.HiddenParams()
	literals := tool.BoundLiterals()
	modeLabel := effectLabel(tool.Effect, tool.Idempotent)

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

	if tool.Sig != nil {
		if desc := tool.Sig.Description(); desc != "" {
			for _, line := range strings.Split(desc, "\n") {
				fmt.Fprintf(b, "%s// %s\n", indent, line)
			}
		}
	}

	useMultiLine := len(multiLineParams) > 0
	var paramParts []string
	if tool.Sig != nil {
		for _, p := range tool.Sig.Params() {
			if hidden[p.Name()] {
				continue
			}
			var tsType string
			if litVal, ok := literals[p.Name()]; ok {
				tsType = literalToTS(litVal)
			} else if sharedName, ok := paramTypeOverrides[tool.Name+"."+p.Name()]; ok {
				tsType = sharedName
			} else if props := p.Type().ObjectProperties(); len(props) > 0 && hasDescriptions(props) {
				tsType = renderReturnTypeInlineComments(p.Type())
			} else {
				tsType = p.Type().ToTS()
			}
			name := p.Name()
			if p.Optional() {
				name += "?"
			}
			desc := p.Description()
			if useMultiLine {
				paramParts = append(paramParts, name+": "+tsType)
			} else if desc != "" && !strings.Contains(desc, "\n") {
				paramParts = append(paramParts, fmt.Sprintf("/** %s */ %s: %s", desc, name, tsType))
			} else {
				paramParts = append(paramParts, fmt.Sprintf("%s: %s", name, tsType))
			}
		}
	}

	returnType := "string"
	if tool.Sig != nil {
		if rt := tool.Sig.Return(); rt != nil {
			shape, _ := sdkReturnShape(rt)
			if shape == nil {
				shape = rt.UnwrapPromise()
			}
			if info, ok := returnTypes[tool.Name]; ok {
				switch info.mode {
				case "named":
					returnType = info.typeName
				case "inline-comments":
					returnType = renderReturnTypeInlineComments(shape)
				default:
					returnType = shape.ToTS()
				}
			} else {
				returnType = shape.ToTS()
			}
		}
	}

	modeTrail := ""
	if modeLabel != "" {
		modeTrail = " // " + strings.Trim(modeLabel, "()")
	}

	if useMultiLine && len(paramParts) > 0 {
		fmt.Fprintf(b, "%sfunction %s(\n", indent, method)
		visibleParams := make([]toolbox.FuncParam, 0)
		if tool.Sig != nil {
			for _, p := range tool.Sig.Params() {
				if !hidden[p.Name()] {
					visibleParams = append(visibleParams, p)
				}
			}
		}
		for i, part := range paramParts {
			if i < len(visibleParams) {
				if desc := visibleParams[i].Description(); desc != "" {
					for _, dl := range strings.Split(desc, "\n") {
						fmt.Fprintf(b, "%s  // %s\n", indent, dl)
					}
				}
			}
			trailing := ","
			if i == len(paramParts)-1 {
				trailing = ""
			}
			fmt.Fprintf(b, "%s  %s%s\n", indent, part, trailing)
		}
		fmt.Fprintf(b, "%s): %s;%s\n", indent, returnType, modeTrail)
		return
	}

	fmt.Fprintf(b, "%sfunction %s(%s): %s;%s\n", indent, method, strings.Join(paramParts, ", "), returnType, modeTrail)
}

func wrapNamespacePath(segments []string, body string) string {
	if len(segments) == 0 {
		return body
	}
	var b strings.Builder
	for i, segment := range segments {
		indent := strings.Repeat("  ", i)
		if i == 0 {
			fmt.Fprintf(&b, "declare namespace %s {\n", segment)
		} else {
			fmt.Fprintf(&b, "%snamespace %s {\n", indent, segment)
		}
	}
	if body != "" {
		b.WriteString(indentLines(body, len(segments)))
		b.WriteString("\n")
	}
	for i := len(segments) - 1; i >= 0; i-- {
		indent := strings.Repeat("  ", i)
		fmt.Fprintf(&b, "%s}", indent)
		if i > 0 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func namespaceSegments(packageName string) []string {
	parts := strings.Split(strings.TrimSpace(packageName), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, sanitizeIdentifierSegment(part))
	}
	if len(out) == 0 {
		return []string{"pkg"}
	}
	return out
}

func toolSegments(toolName string) []string {
	return splitDotted(toolName)
}

func splitDotted(value string) []string {
	raw := strings.Split(strings.TrimSpace(value), ".")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func indentLines(body string, levels int) string {
	prefix := strings.Repeat("  ", levels)
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func sanitizeIdentifierSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "pkg"
	}
	var out []rune
	upperNext := false
	for _, r := range segment {
		switch {
		case r == '-' || r == '.' || r == ' ' || r == '/':
			upperNext = true
		case len(out) == 0 && (unicode.IsLetter(r) || r == '_' || r == '$'):
			out = append(out, r)
			upperNext = false
		case len(out) == 0 && unicode.IsDigit(r):
			out = append(out, '_', r)
			upperNext = false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$':
			if upperNext && unicode.IsLetter(r) {
				out = append(out, unicode.ToUpper(r))
			} else {
				out = append(out, r)
			}
			upperNext = false
		default:
			if !upperNext {
				out = append(out, '_')
			}
			upperNext = false
		}
	}
	if len(out) == 0 {
		return "pkg"
	}
	return string(out)
}

func lastSegment(name string) string {
	parts := splitDotted(name)
	if len(parts) == 0 {
		return name
	}
	return parts[len(parts)-1]
}

// hasDescriptions reports whether any property has a description.
func hasDescriptions(props []toolbox.PropertyInfo) bool {
	for _, p := range props {
		if p.Description != "" {
			return true
		}
	}
	return false
}

// effectLabel returns the parenthesized label for the effect and
// idempotent flag, or "" if no effect is set.
func effectLabel(effect tooldef.Effect, idempotent *bool) string {
	isIdempotent := idempotent != nil && *idempotent
	switch effect {
	case tooldef.EffectReadOnly:
		return "(readonly)"
	case tooldef.EffectReversible:
		if isIdempotent {
			return "(reversible, idempotent)"
		}
		return "(reversible)"
	case tooldef.EffectIrreversible:
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
// collectSplitDeclarations gathers type declarations split into param-sourced
// and return-sourced lists. Param types are emitted before return types to
// match how function signatures read (args before return values).
func collectSplitDeclarations(tools []toolset.AgentTool) (paramLines, returnLines []string) {
	// Collect all definition types for description enhancement.
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

	seen := map[string]bool{}
	addLine := func(line string, target *[]string) {
		enhanced := enhanceDeclWithDescriptions(line, defTypes)
		if enhanced != "" && !seen[enhanced] {
			seen[enhanced] = true
			*target = append(*target, enhanced)
		} else if !seen[line] {
			seen[line] = true
			*target = append(*target, line)
		}
	}

	for _, tool := range tools {
		// Param declarations → paramLines
		if pt := tool.ParamsType(); pt != nil {
			if decls := pt.Declarations(); decls != "" {
				for _, line := range strings.Split(strings.TrimRight(decls, "\n"), "\n") {
					if line != "" {
						addLine(line, &paramLines)
					}
				}
			}
		}
		// Return declarations → returnLines
		if tool.Sig != nil {
			if rt := tool.Sig.Return(); rt != nil {
				if decls := rt.Declarations(); decls != "" {
					for _, line := range strings.Split(strings.TrimRight(decls, "\n"), "\n") {
						if line != "" {
							addLine(line, &returnLines)
						}
					}
				}
			}
		}
	}
	sort.Strings(paramLines)
	sort.Strings(returnLines)
	return paramLines, returnLines
}

// collectUniqueDeclarations gathers type declarations used by the emitted SDK.
func collectUniqueDeclarations(tools []toolset.AgentTool, returnTypes map[string]returnTypeInfo) []string {
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
		var sources []*toolbox.TSType
		if pt := tool.ParamsType(); pt != nil {
			sources = append(sources, pt)
		}
		if shouldEmitReturnDeclarations(tool, returnTypes) && tool.Sig != nil {
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

	// Prepend type-level description if available.
	typeDesc := defType.Description()

	if hasMultiLineDesc {
		var b strings.Builder
		if typeDesc != "" {
			fmt.Fprintf(&b, "// %s\n", typeDesc)
		}
		fmt.Fprintf(&b, "interface %s {\n", typeName)
		for _, p := range props {
			if p.Description != "" {
				for _, dl := range strings.Split(p.Description, "\n") {
					fmt.Fprintf(&b, "  // %s\n", dl)
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
	typeAlias := fmt.Sprintf("type %s = { %s };", typeName, strings.Join(parts, "; "))
	if typeDesc != "" {
		return fmt.Sprintf("// %s\n%s", typeDesc, typeAlias)
	}
	return typeAlias
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

func shouldEmitReturnDeclarations(tool toolset.AgentTool, returnTypes map[string]returnTypeInfo) bool {
	if tool.Sig == nil || tool.Sig.Return() == nil {
		return false
	}
	shape, refName := sdkReturnShape(tool.Sig.Return())
	if refName != "" && shape != nil && shape.IsObject() {
		info, ok := returnTypes[tool.Name]
		return ok && info.mode == "named" && info.typeName == refName
	}
	return true
}

func preferredSharedReturnTypeName(key string, paramDefNames map[string]string, refNames map[string]map[string]int, fallbackNames map[string]string) string {
	if defName, ok := paramDefNames[key]; ok && defName != "" {
		return defName
	}
	if names := refNames[key]; len(names) == 1 {
		for name := range names {
			if name != "" {
				return name
			}
		}
	}
	if fallback := fallbackNames[key]; fallback != "" {
		return fallback
	}
	return "Result"
}

func sdkReturnShape(rt *toolbox.TSType) (*toolbox.TSType, string) {
	if rt == nil {
		return nil, ""
	}
	unwrapped := rt.UnwrapPromise()
	if unwrapped == nil {
		return nil, ""
	}
	if props := unwrapped.ObjectProperties(); len(props) > 0 {
		return unwrapped, ""
	}
	name := strings.TrimSpace(unwrapped.ToTS())
	if name == "" {
		return unwrapped, ""
	}
	defs := unwrapped.DefinitionTypes()
	if def, ok := defs[name]; ok && def != nil && len(def.ObjectProperties()) > 0 {
		return def, name
	}
	return unwrapped, ""
}
