package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestMCPServerListsVisibleInvokeTools(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
	assertContains(t, names, "calc.asyncAdd")
}

func TestMCPServerCallsInvokeForTool(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": 5,
		"b": 5,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	structured := mcptest.StructuredMap(t, result)
	if got := structured["tool"]; got != "calc.add" {
		t.Fatalf("expected tool calc.add, got %#v", got)
	}
	if got := structured["result"]; got != "10" {
		t.Fatalf("expected result 10, got %#v", got)
	}
	if got := structured["status"]; got != "stub-invoked" {
		t.Fatalf("expected status stub-invoked, got %#v", got)
	}
}

func TestMCPServerCallsInvokeForDifferentArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.asyncAdd", map[string]any{
		"a": 7,
		"b": 4,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	structured := mcptest.StructuredMap(t, result)
	if got := structured["result"]; got != "11" {
		t.Fatalf("expected result 11, got %#v", got)
	}
}

func TestMCPServerCallsInvokeForStringAndNumberArgs(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(tooltest.CalcToolset(t)))

	result := h.CallTool("calc.add", map[string]any{
		"a": "6",
		"b": 3,
	})
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected error content")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	if !strings.Contains(text.Text, "typescript check failed") {
		t.Fatalf("expected typecheck failure, got %#v", text.Text)
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
}
