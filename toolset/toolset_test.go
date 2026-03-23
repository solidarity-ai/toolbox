package toolset

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestBuilderAddFromDirLoadsPackageName(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	pkgs := ts.Packages()
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Name != "calc" {
		t.Fatalf("expected package name %q, got %q", "calc", pkgs[0].Name)
	}
}

func TestResolveEmptyConfigProducesAgentView(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	resolved, err := ts.Resolve(Config{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	view := resolved.AgentView()

	if len(view.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(view.Tools))
	}

	// Sort for deterministic checking.
	sort.Slice(view.Tools, func(i, j int) bool {
		return view.Tools[i].Name < view.Tools[j].Name
	})

	if view.Tools[0].Name != "calc.add" {
		t.Errorf("expected first tool calc.add, got %q", view.Tools[0].Name)
	}

	props := schemaProperties(t, view.Tools[0].ParamsSchema)
	if _, ok := props["a"]; !ok {
		t.Error("param a not found in calc.add ParamsSchema")
	}
	if _, ok := props["b"]; !ok {
		t.Error("param b not found in calc.add ParamsSchema")
	}
}

func TestResolveHiddenParamRemovesFromAgentView(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	resolved, err := ts.Resolve(Config{
		Tools: []BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]Binding{
					"b": {Value: "'42'", Hidden: true},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")

	props := schemaProperties(t, addTool.ParamsSchema)
	if _, ok := props["b"]; ok {
		t.Error("param b should be hidden from AgentView but was found")
	}
	if _, ok := props["a"]; !ok {
		t.Error("param a should still be visible but was not found")
	}

	// b should also be removed from required.
	required := schemaRequired(addTool.ParamsSchema)
	for _, name := range required {
		if name == "b" {
			t.Error("param b should be removed from required but was found")
		}
	}
}

func TestResolveValueBindingKeepsParamVisible(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	// Value binding without Hidden=true should keep the param visible.
	resolved, err := ts.Resolve(Config{
		Tools: []BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]Binding{
					"b": {Value: "params.b"},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	view := resolved.AgentView()
	addTool := findAgentTool(t, view, "calc.add")

	props := schemaProperties(t, addTool.ParamsSchema)
	if _, ok := props["b"]; !ok {
		t.Error("param b should still be visible when not hidden")
	}
}

func TestResolveInvalidCELExpressionReturnsError(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	_, err := ts.Resolve(Config{
		Tools: []BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]Binding{
					"b": {Value: "invalid.syntax.!!!"},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid CEL expression, got nil")
	}
}

func TestResolveCheckExpressionCompilesAtResolveTime(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "calc")

	ts := New()
	if err := ts.AddFromDir(dir); err != nil {
		t.Fatalf("add package dir: %v", err)
	}

	// A valid check expression should compile without error.
	_, err := ts.Resolve(Config{
		Tools: []BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]Binding{
					"a": {Check: "params.a > 0"},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve with check expression: %v", err)
	}
}

// findAgentTool returns the AgentTool with the given name, or fails the test.
func findAgentTool(t *testing.T, view AgentView, name string) AgentTool {
	t.Helper()
	for _, tool := range view.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in AgentView", name)
	return AgentTool{}
}

// schemaProperties extracts the "properties" map from a JSON Schema.
func schemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("ParamsSchema has no properties")
	}
	return props
}

// schemaRequired extracts the "required" array from a JSON Schema.
func schemaRequired(schema map[string]any) []string {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
