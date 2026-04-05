package toolset_test

import (
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func TestValidateCallInjectsHiddenParam(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
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

	prepared := calcToolset(t, cfg)

	fullParams, err := prepared.ValidateCall("calc.add", map[string]any{"b": 3})
	if err != nil {
		t.Fatalf("validate call: %v", err)
	}

	if fullParams["a"] != int64(42) {
		t.Fatalf("expected hidden param a=42, got %v (%T)", fullParams["a"], fullParams["a"])
	}
	if fullParams["b"] != 3 {
		t.Fatalf("expected visible param b=3, got %v", fullParams["b"])
	}
}

func TestValidateCallValueBindingOverridesAgentParam(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
			"forced_a": 99,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "context.forced_a"},
				},
			},
		},
	}

	prepared := calcToolset(t, cfg)

	fullParams, err := prepared.ValidateCall("calc.add", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("validate call: %v", err)
	}

	// Value binding should override agent-provided value
	if fullParams["a"] != int64(99) {
		t.Fatalf("expected bound param a=99, got %v (%T)", fullParams["a"], fullParams["a"])
	}
}

func TestValidateCallCheckPassesAllowsExecution(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
			"max_value": 100,
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

	prepared := calcToolset(t, cfg)

	fullParams, err := prepared.ValidateCall("calc.add", map[string]any{"a": 50, "b": 2})
	if err != nil {
		t.Fatalf("validate call: %v", err)
	}

	if fullParams["a"] != 50 {
		t.Fatalf("expected param a=50, got %v", fullParams["a"])
	}
}

func TestValidateCallCheckFailsReturnsError(t *testing.T) {
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

	prepared := calcToolset(t, cfg)

	_, err := prepared.ValidateCall("calc.add", map[string]any{"a": 50, "b": 2})
	if err == nil {
		t.Fatal("expected check failure error")
	}
	if !strings.Contains(err.Error(), "check failed") {
		t.Fatalf("expected 'check failed' in error, got: %v", err)
	}
}

func TestValidateCallNoBindingsPassthrough(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{})

	fullParams, err := prepared.ValidateCall("calc.add", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("validate call: %v", err)
	}

	if fullParams["a"] != 1 || fullParams["b"] != 2 {
		t.Fatalf("expected passthrough params, got %v", fullParams)
	}
}

func TestValidateCallUnknownToolErrors(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{})

	_, err := prepared.ValidateCall("nonexistent.tool", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestValidateCallLiteralBinding(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "42", Hidden: true},
				},
			},
		},
	}

	prepared := calcToolset(t, cfg)

	fullParams, err := prepared.ValidateCall("calc.add", map[string]any{"b": 3})
	if err != nil {
		t.Fatalf("validate call: %v", err)
	}

	if fullParams["a"] != int64(42) {
		t.Fatalf("expected literal binding a=42, got %v (%T)", fullParams["a"], fullParams["a"])
	}
}
