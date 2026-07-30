package tool

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ResourceAPITool is the payload-free input used to compile a package's
// resource API. Tool identifies the caller-owned payload by index.
type ResourceAPITool struct {
	Name string
	Uses []ResourceAPIUse
}

// ResourceAPIUse describes how one tool traverses a declared resource.
type ResourceAPIUse struct {
	Path     string
	Params   []string
	Selected bool
}

// ResourceAPIPlan is the single topology consumed by validation, declaration
// generation, and runtime installation.
type ResourceAPIPlan struct {
	Root *ResourceAPINode
}

type ResourceAPINode struct {
	Methods  []ResourceAPIMethod
	Children []ResourceAPIChild
}

type ResourceAPIMethod struct {
	Name           string
	Tool           int
	SelectorParams []string
}

type ResourceAPIChild struct {
	Name     string
	Plain    *ResourceAPINode
	Resource *ResourceAPIResource
}

type ResourceAPIResource struct {
	Callable   bool
	Params     []string
	ParamTool  int
	Collection *ResourceAPINode
	Member     *ResourceAPINode
}

type resourceAPIBuilderNode struct {
	methods  map[string]ResourceAPIMethod
	children map[string]*resourceAPIBuilderChild
}

type resourceAPIBuilderChild struct {
	plain    *resourceAPIBuilderNode
	resource *resourceAPIBuilderResource
}

type resourceAPIBuilderResource struct {
	selected   bool
	callable   bool
	params     []string
	paramTool  int
	collection *resourceAPIBuilderNode
	member     *resourceAPIBuilderNode
}

// CompileResourceAPI builds and validates the generated JavaScript API once,
// without retaining declaration or runtime payloads.
func CompileResourceAPI(tools []ResourceAPITool) (*ResourceAPIPlan, error) {
	root := &resourceAPIBuilderNode{}
	hasResources := false
	for toolIndex, input := range tools {
		segments := splitResourcePath(input.Name)
		if len(segments) == 0 {
			continue
		}
		uses := make(map[string]ResourceAPIUse, len(input.Uses))
		selectorParams := map[string]bool{}
		for _, use := range input.Uses {
			hasResources = true
			uses[use.Path] = use
			if use.Selected {
				for _, param := range use.Params {
					selectorParams[param] = true
				}
			}
		}

		node := root
		path := make([]string, 0, len(segments)-1)
		for _, segment := range segments[:len(segments)-1] {
			path = append(path, segment)
			if node.children == nil {
				node.children = map[string]*resourceAPIBuilderChild{}
			}
			child := node.children[segment]
			if child == nil {
				child = &resourceAPIBuilderChild{}
				node.children[segment] = child
			}
			if use, ok := uses[strings.Join(path, ".")]; ok {
				if child.plain != nil {
					return nil, fmt.Errorf("generated API property %q is both a resource and a namespace", segment)
				}
				if child.resource == nil {
					child.resource = &resourceAPIBuilderResource{
						paramTool:  toolIndex,
						collection: &resourceAPIBuilderNode{},
						member:     &resourceAPIBuilderNode{},
					}
				}
				if use.Selected {
					if child.resource.selected && !slices.Equal(child.resource.params, use.Params) {
						return nil, fmt.Errorf("resource %q uses incompatible selector parameters %v and %v", use.Path, child.resource.params, use.Params)
					}
					if !child.resource.selected {
						child.resource.selected = true
						child.resource.callable = len(use.Params) > 0
						child.resource.params = slices.Clone(use.Params)
						child.resource.paramTool = toolIndex
					}
					if child.resource.callable {
						node = child.resource.member
					} else {
						node = child.resource.collection
					}
				} else {
					node = child.resource.collection
				}
				continue
			}
			if child.resource != nil {
				return nil, fmt.Errorf("generated API property %q is both a resource and a namespace", segment)
			}
			if child.plain == nil {
				child.plain = &resourceAPIBuilderNode{}
			}
			node = child.plain
		}

		if node.methods == nil {
			node.methods = map[string]ResourceAPIMethod{}
		}
		method := segments[len(segments)-1]
		if _, exists := node.methods[method]; exists {
			return nil, fmt.Errorf("duplicate generated API method %q", input.Name)
		}
		selected := make([]string, 0, len(selectorParams))
		for name := range selectorParams {
			selected = append(selected, name)
		}
		sort.Strings(selected)
		node.methods[method] = ResourceAPIMethod{Name: method, Tool: toolIndex, SelectorParams: selected}
	}
	if err := validateResourceAPIBuilderNode(root, false, hasResources); err != nil {
		return nil, err
	}
	return &ResourceAPIPlan{Root: freezeResourceAPINode(root)}, nil
}

func validateResourceAPIBuilderNode(node *resourceAPIBuilderNode, callable, reserved bool) error {
	members := map[string]bool{}
	for name := range node.methods {
		if reserved && CallableResourceMemberReserved(name) {
			return fmt.Errorf("generated resource API member %q is reserved", name)
		}
		members[name] = true
	}
	for name := range node.children {
		if reserved && CallableResourceMemberReserved(name) {
			return fmt.Errorf("generated resource API member %q is reserved", name)
		}
		if members[name] {
			return fmt.Errorf("generated API property %q conflicts with a method on the same surface", name)
		}
		members[name] = true
	}
	if callable {
		for name := range members {
			if alias, ok := CallableResourceIntrinsicAlias(name); ok && members[alias] {
				return fmt.Errorf("generated API member %q conflicts with intrinsic escape method for %q on the same callable resource collection surface", alias, name)
			}
		}
	}
	for _, child := range node.children {
		if child.resource != nil {
			if err := validateResourceAPIBuilderNode(child.resource.collection, child.resource.callable, reserved); err != nil {
				return err
			}
			if err := validateResourceAPIBuilderNode(child.resource.member, false, reserved); err != nil {
				return err
			}
		} else if err := validateResourceAPIBuilderNode(child.plain, false, reserved); err != nil {
			return err
		}
	}
	return nil
}

func freezeResourceAPINode(node *resourceAPIBuilderNode) *ResourceAPINode {
	if node == nil {
		return nil
	}
	out := &ResourceAPINode{}
	methodNames := make([]string, 0, len(node.methods))
	for name := range node.methods {
		methodNames = append(methodNames, name)
	}
	sort.Strings(methodNames)
	for _, name := range methodNames {
		method := node.methods[name]
		method.SelectorParams = slices.Clone(method.SelectorParams)
		out.Methods = append(out.Methods, method)
	}
	childNames := make([]string, 0, len(node.children))
	for name := range node.children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		child := node.children[name]
		frozen := ResourceAPIChild{Name: name, Plain: freezeResourceAPINode(child.plain)}
		if child.resource != nil {
			frozen.Resource = &ResourceAPIResource{
				Callable:   child.resource.callable,
				Params:     slices.Clone(child.resource.params),
				ParamTool:  child.resource.paramTool,
				Collection: freezeResourceAPINode(child.resource.collection),
				Member:     freezeResourceAPINode(child.resource.member),
			}
		}
		out.Children = append(out.Children, frozen)
	}
	return out
}
