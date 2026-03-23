package toolset

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCallNoBindingsPassesThrough(t *testing.T) {
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

	params := map[string]any{"a": 1.0, "b": 2.0}
	got, err := resolved.ValidateCall("calc.add", params)
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}

	// With no bindings, output should match input.
	if got["a"] != 1.0 || got["b"] != 2.0 {
		t.Errorf("expected passthrough params, got %v", got)
	}
}

func TestValidateCallInjectsHiddenParam(t *testing.T) {
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
					"b": {Value: "context.default_b", Hidden: true},
				},
			},
		},
		Context: map[string]any{"default_b": 42.0},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Agent only provides "a"; "b" is injected from context.
	params := map[string]any{"a": 1.0}
	got, err := resolved.ValidateCall("calc.add", params)
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}

	if got["a"] != 1.0 {
		t.Errorf("expected a=1, got %v", got["a"])
	}
	if got["b"] != 42.0 {
		t.Errorf("expected b=42 (injected), got %v", got["b"])
	}
}

func TestValidateCallCheckExpressionPasses(t *testing.T) {
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
					"a": {Check: "params.a > 0"},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	params := map[string]any{"a": 5.0, "b": 2.0}
	got, err := resolved.ValidateCall("calc.add", params)
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}

	if got["a"] != 5.0 {
		t.Errorf("expected a=5, got %v", got["a"])
	}
}

func TestValidateCallCheckExpressionFails(t *testing.T) {
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
					"a": {Check: "params.a > 0"},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// a=-1 violates check "params.a > 0"
	params := map[string]any{"a": -1.0, "b": 2.0}
	_, err = resolved.ValidateCall("calc.add", params)
	if err == nil {
		t.Fatal("expected check failure, got nil")
	}
	if !strings.Contains(err.Error(), "check failed") {
		t.Errorf("expected 'check failed' in error, got %q", err.Error())
	}
}

func TestValidateCallUnknownToolReturnsError(t *testing.T) {
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

	_, err = resolved.ValidateCall("nonexistent.tool", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestValidateCallLiteralValueBinding(t *testing.T) {
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
					"b": {Value: "99", Hidden: true},
				},
			},
		},
		Context: map[string]any{},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	params := map[string]any{"a": 1.0}
	got, err := resolved.ValidateCall("calc.add", params)
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}

	if got["a"] != 1.0 {
		t.Errorf("expected a=1, got %v", got["a"])
	}
	// CEL literal 99 evaluates to int64.
	if got["b"] != int64(99) {
		t.Errorf("expected b=99 (literal), got %v (%T)", got["b"], got["b"])
	}
}
