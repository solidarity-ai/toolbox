package tool

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/microsoft/typescript-go/toolbox"
)

var callableResourceIntrinsicAliases = map[string]string{
	"name":        "_name",
	"length":      "_length",
	"prototype":   "_prototype",
	"arguments":   "_arguments",
	"caller":      "_caller",
	"apply":       "_apply",
	"bind":        "_bind",
	"call":        "_call",
	"constructor": "_constructor",
}

var callableResourceReservedMembers = map[string]bool{
	"then":                 true,
	"__proto__":            true,
	"toString":             true,
	"toLocaleString":       true,
	"valueOf":              true,
	"hasOwnProperty":       true,
	"isPrototypeOf":        true,
	"propertyIsEnumerable": true,
	"__defineGetter__":     true,
	"__defineSetter__":     true,
	"__lookupGetter__":     true,
	"__lookupSetter__":     true,
}

// CallableResourceIntrinsicAlias returns the escape-hatch method generated
// when a tool deliberately shadows an intrinsic property of a callable
// resource selector.
func CallableResourceIntrinsicAlias(name string) (string, bool) {
	alias, ok := callableResourceIntrinsicAliases[name]
	return alias, ok
}

// CallableResourceMemberReserved reports names whose JavaScript semantics
// cannot safely be represented as methods on a callable resource selector.
func CallableResourceMemberReserved(name string) bool {
	return callableResourceReservedMembers[name]
}

// ResolveResourceUses determines where a tool lives relative to the package's
// resource selectors. A selector is selected when all of its parameters are
// present in the tool signature, and remains on the callable collection
// surface when none are present. Selected parameters must form the signature's
// ordered prefix, following the resource chain and each declaration's order.
func ResolveResourceUses(resources []Resource, toolName string, sig *toolbox.FuncSignature) ([]ResourceUse, error) {
	toolParts := splitResourcePath(toolName)
	if len(toolParts) == 0 {
		return nil, nil
	}
	namespaceParts := toolParts[:len(toolParts)-1]
	applicable := make([]Resource, 0)
	for _, resource := range resources {
		parts := splitResourcePath(resource.Path)
		if pathPrefix(parts, namespaceParts) {
			applicable = append(applicable, resource)
		}
	}
	sort.Slice(applicable, func(i, j int) bool {
		left := len(splitResourcePath(applicable[i].Path))
		right := len(splitResourcePath(applicable[j].Path))
		if left != right {
			return left < right
		}
		return applicable[i].Path < applicable[j].Path
	})

	var signatureParams []toolbox.FuncParam
	params := map[string]*toolbox.FuncParam{}
	if sig != nil {
		signatureParams = sig.Params()
		for _, param := range signatureParams {
			param := param
			params[param.Name()] = &param
		}
	}

	uses := make([]ResourceUse, 0, len(applicable))
	selectedParamNames := make([]string, 0)
	encounteredCollection := false
	for _, resource := range applicable {
		present := 0
		for _, selector := range resource.Params {
			if _, ok := params[selector.Name]; ok {
				present++
			}
		}
		if present != 0 && present != len(resource.Params) {
			return nil, fmt.Errorf("tool %q partially selects resource %q: found %d of %d selector parameters", toolName, resource.Path, present, len(resource.Params))
		}
		selected := present == len(resource.Params) && len(resource.Params) > 0
		if selected && encounteredCollection {
			return nil, fmt.Errorf("tool %q selects resource %q after leaving an ancestor resource unselected", toolName, resource.Path)
		}
		if !selected {
			encounteredCollection = true
		}
		if selected {
			for _, selector := range resource.Params {
				if params[selector.Name].Optional() {
					return nil, fmt.Errorf("tool %q resource %q selector parameter %q must be required", toolName, resource.Path, selector.Name)
				}
				selectedParamNames = append(selectedParamNames, selector.Name)
			}
		}
		uses = append(uses, ResourceUse{Resource: cloneResource(resource), Selected: selected})
	}
	for i, use := range uses {
		if !use.Selected && i != len(uses)-1 {
			return nil, fmt.Errorf("tool %q leaves non-deepest resource %q unselected", toolName, use.Resource.Path)
		}
	}
	for index, expected := range selectedParamNames {
		if index >= len(signatureParams) || signatureParams[index].Name() != expected {
			actual := make([]string, 0, min(len(signatureParams), len(selectedParamNames)))
			for _, param := range signatureParams[:min(len(signatureParams), len(selectedParamNames))] {
				actual = append(actual, param.Name())
			}
			return nil, fmt.Errorf("tool %q resource selector parameters must be the ordered signature prefix %v; got %v", toolName, selectedParamNames, actual)
		}
	}
	return uses, nil
}

// ResolvePackageResources validates signature use across a package and stores
// the derived, non-serialized resource uses on each tool.
func ResolvePackageResources(pkg *Package) error {
	if pkg == nil || len(pkg.Resources) == 0 {
		return nil
	}
	selectedByPath := map[string]bool{}
	typesByParam := map[string]string{}
	for i := range pkg.Tools {
		tool := &pkg.Tools[i]
		name := toolNameFromEntry(tool.EntryTS)
		uses, err := ResolveResourceUses(pkg.Resources, name, tool.Sig)
		if err != nil {
			return err
		}
		tool.ResourceUses = uses
		params := map[string]*toolbox.FuncParam{}
		if tool.Sig != nil {
			for _, param := range tool.Sig.Params() {
				param := param
				params[param.Name()] = &param
			}
		}
		for _, use := range uses {
			if !use.Selected {
				continue
			}
			selectedByPath[use.Resource.Path] = true
			for _, selector := range use.Resource.Params {
				param := params[selector.Name]
				key := use.Resource.Path + "\x00" + selector.Name
				typeName := param.Type().ToTS()
				if previous, ok := typesByParam[key]; ok && previous != typeName {
					return fmt.Errorf("resource %q selector parameter %q has incompatible types %q and %q", use.Resource.Path, selector.Name, previous, typeName)
				}
				typesByParam[key] = typeName
			}
		}
	}
	for _, resource := range pkg.Resources {
		if !selectedByPath[resource.Path] {
			return fmt.Errorf("resource %q is never selected by a tool signature", resource.Path)
		}
	}
	apiTools := make([]ResourceAPITool, 0, len(pkg.Tools))
	for _, packageTool := range pkg.Tools {
		input := ResourceAPITool{Name: toolNameFromEntry(packageTool.EntryTS)}
		for _, use := range packageTool.ResourceUses {
			apiUse := ResourceAPIUse{Path: use.Resource.Path, Selected: use.Selected}
			for _, param := range use.Resource.Params {
				apiUse.Params = append(apiUse.Params, param.Name)
			}
			input.Uses = append(input.Uses, apiUse)
		}
		apiTools = append(apiTools, input)
	}
	_, err := CompileResourceAPI(apiTools)
	return err
}

func cloneResource(resource Resource) Resource {
	resource.Params = append([]ResourceParam(nil), resource.Params...)
	return resource
}

// CloneResourceUses returns a deep copy of resource uses and selector params.
func CloneResourceUses(in []ResourceUse) []ResourceUse {
	out := slices.Clone(in)
	for i := range out {
		out[i].Resource = cloneResource(out[i].Resource)
	}
	return out
}

func splitResourcePath(path string) []string {
	raw := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func pathPrefix(prefix, path []string) bool {
	if len(prefix) == 0 || len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if prefix[i] != path[i] {
			return false
		}
	}
	return true
}

func toolNameFromEntry(entry string) string {
	base := strings.TrimSuffix(filepath.Base(entry), filepath.Ext(entry))
	parts := strings.Split(base, ".")
	for i, part := range parts {
		parts[i] = resourceKebabToCamel(part)
	}
	return strings.Join(parts, ".")
}

func resourceKebabToCamel(value string) string {
	var out []rune
	upperNext := false
	for _, r := range value {
		if r == '-' {
			upperNext = true
			continue
		}
		if upperNext {
			out = append(out, unicode.ToUpper(r))
			upperNext = false
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
