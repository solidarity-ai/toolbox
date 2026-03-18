package mcpserver_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestMCPServerListsVisibleInvokeTools(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(testToolset()))
	names := h.ToolNames()

	assertContains(t, names, "calc.add")
}

func TestMCPServerCallsInvokeForTool(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New(testToolset()))

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

func testToolset() toolset.ResolvedToolset {
	return toolset.NewResolvedToolset([]toolset.Tool{
		{Name: "calc.add", Description: "Add two numbers"},
	})
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
