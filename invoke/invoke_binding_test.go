package invoke_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestRunWithHiddenParamBinding(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
			"fixed_a": 10,
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

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), cfg)

	// Agent only provides b=5; a=10 is injected from context
	result, err := invoke.Run(prepared, "calc.add", map[string]any{"b": 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result != "15" {
		t.Fatalf("expected 15 (10+5), got %q", result)
	}
}

func TestRunWithHiddenParamBindingOverridesAgentValue(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
			"fixed_a": 10,
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

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), cfg)

	// Agent tries to pass a=999 for a hidden param — binding should override it
	result, err := invoke.Run(prepared, "calc.add", map[string]any{"a": 999, "b": 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Should be 15 (10+5), not 1004 (999+5) — the bound value wins
	if result != "15" {
		t.Fatalf("expected 15 (bound 10+5), got %q — agent value was not overridden", result)
	}
}

func TestRunWithCheckExpressionBlocksCall(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
			"max_value": 10,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Check: "params.a < context.max_value"},
				},
			},
		},
	}

	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), cfg)

	// a=50 exceeds max_value=10, should be blocked
	_, err := invoke.Run(prepared, "calc.add", map[string]any{"a": 50, "b": 5})
	if err == nil {
		t.Fatal("expected check failure error")
	}
}
