package toolset_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func TestAgentViewNoBindingsShowsAllParams(t *testing.T) {
	t.Parallel()

	resolved := calcToolset(t, toolset.Config{})
	view := resolved.AgentView()

	if len(view.Tools) == 0 {
		t.Fatal("expected at least one tool in AgentView")
	}

	// calc.add has params {a: number, b: number} — both should be visible
	addTool := findAgentTool(t, view, "calc.add")
	if addTool.ParamsSchema == nil {
		t.Fatal("expected ParamsSchema for calc.add")
	}

	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["a"]; !ok {
		t.Fatal("expected param 'a' in ParamsSchema")
	}
	if _, ok := props["b"]; !ok {
		t.Fatal("expected param 'b' in ParamsSchema")
	}
}

func TestAgentViewHiddenParamRemovedFromSchema(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
			"fixed_a": 42,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "context.fixed_a", Hidden: true},
				},
			},
		},
	}

	resolved := calcToolset(t, cfg)
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}

	if _, ok := props["a"]; ok {
		t.Fatal("hidden param 'a' should not appear in AgentView schema")
	}
	if _, ok := props["b"]; !ok {
		t.Fatal("visible param 'b' should still appear in AgentView schema")
	}

	// required should also not include 'a'
	if required, ok := addTool.ParamsSchema["required"].([]any); ok {
		for _, r := range required {
			if r == "a" {
				t.Fatal("hidden param 'a' should not be in required list")
			}
		}
	}
}

func TestAgentViewCheckExpressionStored(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
			"allowed_max": 100,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Check: "params.a < context.allowed_max"},
				},
			},
		},
	}

	// Should resolve without error — check expressions compile at resolve time
	resolved := calcToolset(t, cfg)
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	// Both params should still be visible (check doesn't hide)
	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["a"]; !ok {
		t.Fatal("param 'a' should remain visible with check binding")
	}
}

func TestResolveInvalidCELExpressionErrors(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "invalid!!!syntax"},
				},
			},
		},
	}

	builder := calcBuilder(t)
	_, err := builder.Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CEL expression")
	}
}

func TestAgentViewReadOnlyFlag(t *testing.T) {
	t.Parallel()

	resolved := calcToolset(t, toolset.Config{})
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	if !addTool.ReadOnly {
		t.Fatal("expected calc.add to be read-only (accessMode=readOnly)")
	}
}

// helpers

func calcBuilder(t testing.TB) *toolset.Builder {
	t.Helper()
	builder := toolset.New()
	if err := builder.AddFromDir(calcFixtureDir()); err != nil {
		t.Fatalf("add calc dir: %v", err)
	}
	return builder
}

func calcToolset(t testing.TB, cfg toolset.Config) toolset.ResolvedToolset {
	t.Helper()
	builder := calcBuilder(t)
	resolved, err := builder.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}

func findAgentTool(t testing.TB, view toolset.AgentView, name string) toolset.AgentTool {
	t.Helper()
	for _, tool := range view.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in AgentView", name)
	return toolset.AgentTool{}
}

func calcFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "calc")
}
