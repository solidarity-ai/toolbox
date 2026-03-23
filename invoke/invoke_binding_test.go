package invoke_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestRunWithHiddenParamBinding(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
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

	builder := toolset.New()
	if err := builder.AddFromDir(calcDir()); err != nil {
		t.Fatalf("add calc dir: %v", err)
	}
	resolved, err := builder.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Agent only provides b=5; a=10 is injected from context
	result, err := invoke.Run(resolved, "calc.add", map[string]any{"b": 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result != "15" {
		t.Fatalf("expected 15 (10+5), got %q", result)
	}
}

func TestRunWithCheckExpressionBlocksCall(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
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

	builder := toolset.New()
	if err := builder.AddFromDir(calcDir()); err != nil {
		t.Fatalf("add calc dir: %v", err)
	}
	resolved, err := builder.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// a=50 exceeds max_value=10, should be blocked
	_, err = invoke.Run(resolved, "calc.add", map[string]any{"a": 50, "b": 5})
	if err == nil {
		t.Fatal("expected check failure error")
	}
}

func calcDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "calc")
}
