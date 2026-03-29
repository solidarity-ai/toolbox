package codemodemcp_test

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
)

func TestMCPServerListsDiscoveryAndActionTools(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())
	names := h.ToolNames()

	assertContains(t, names, codemodemcp.ToolDiscoveryExecute)
	assertContains(t, names, codemodemcp.ToolActionExecute)
}

func TestMCPServerCallsToolDiscoveryExecute(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())

	result := h.CallTool(codemodemcp.ToolDiscoveryExecute, map[string]any{
		"code": "return discover.find({ task: 'triage zendesk tickets' })",
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	m := resultJSON(t, result)
	if got := m["mode"]; got != "discovery" {
		t.Fatalf("expected mode discovery, got %#v", got)
	}
	if _, ok := m["toolboxID"]; !ok {
		t.Fatal("expected toolboxID in result")
	}
}

func TestMCPServerCallsToolActionExecute(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())

	action := h.CallTool(codemodemcp.ToolActionExecute, map[string]any{
		"toolboxID": "tbx_123",
		"code":      "return await tools.slack.send('hello')",
	})
	if action.IsError {
		t.Fatalf("expected non-error result")
	}

	m := resultJSON(t, action)
	if got := m["mode"]; got != "action" {
		t.Fatalf("expected mode action, got %#v", got)
	}
	if got := m["toolboxID"]; got != "tbx_123" {
		t.Fatalf("expected toolboxID tbx_123, got %#v", got)
	}
}

func resultJSON(t testing.TB, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text.Text), &m); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, text.Text)
	}
	return m
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
