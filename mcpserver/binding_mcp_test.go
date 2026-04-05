package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestMCP_HiddenParamNotInSchema(t *testing.T) {
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

	h := mcptest.NewHarness(t, mcpserver.New(prepared))

	// Verify "a" is not in the schema
	tools := h.ListTools()
	var calcAdd *mcp.Tool
	for i := range tools.Tools {
		if tools.Tools[i].Name == "calc.add" {
			calcAdd = &tools.Tools[i]
			break
		}
	}
	if calcAdd == nil {
		t.Fatal("expected calc.add in MCP tool list")
	}

	if _, ok := calcAdd.InputSchema.Properties["a"]; ok {
		t.Fatal("hidden param 'a' should NOT appear in MCP tool schema")
	}

	// Call with only b — hidden a=10 should be injected
	result := h.CallTool("calc.add", map[string]any{"b": 5})
	if result.IsError {
		text, _ := mcp.AsTextContent(result.Content[0])
		t.Fatalf("expected non-error result, got: %s", text.Text)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if text.Text != "15" {
		t.Fatalf("expected 15 (10+5), got %q", text.Text)
	}
}

func TestMCP_HiddenParamBindingOverridesAgentValue(t *testing.T) {
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

	h := mcptest.NewHarness(t, mcpserver.New(prepared))

	// Agent tries to pass a=999 — hidden binding should override
	result := h.CallTool("calc.add", map[string]any{"a": 999, "b": 5})
	if result.IsError {
		text, _ := mcp.AsTextContent(result.Content[0])
		t.Fatalf("expected non-error result, got: %s", text.Text)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected text content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	// Should be 15 (10+5), not 1004 (999+5)
	if text.Text != "15" {
		t.Fatalf("expected 15 (bound 10+5), got %q — agent value was not overridden", text.Text)
	}
}

func TestMCP_CheckExpressionBlocksCall(t *testing.T) {
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

	h := mcptest.NewHarness(t, mcpserver.New(prepared))

	// a=50 exceeds max_value=10, should be blocked
	result := h.CallTool("calc.add", map[string]any{"a": 50, "b": 5})
	if !result.IsError {
		t.Fatal("expected check failure error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected error content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if !strings.Contains(text.Text, "check") {
		t.Fatalf("expected check failure message, got: %s", text.Text)
	}
}
