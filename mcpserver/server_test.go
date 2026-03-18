package mcpserver_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
)

func TestMCPServerListsDiscoveryAndActionTools(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New())
	names := h.ToolNames()

	assertContains(t, names, mcpserver.ToolDiscoveryExecute)
	assertContains(t, names, mcpserver.ToolActionExecute)
}

func TestMCPServerCallsToolDiscoveryExecute(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New())

	result := h.CallTool(mcpserver.ToolDiscoveryExecute, map[string]any{
		"code": "return discover.find({ task: 'triage zendesk tickets' })",
	})

	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	structured := mcptest.StructuredMap(t, result)
	// TODO: revisit the strucute of this map
	if got := structured["mode"]; got != "discovery" {
		t.Fatalf("expected mode discovery, got %#v", got)
	}
	if got := structured["toolboxID"]; got != "tbx_test_snapshot" {
		t.Fatalf("expected toolboxID tbx_test_snapshot, got %#v", got)
	}
}

func TestMCPServerCallsToolActionExecute(t *testing.T) {
	h := mcptest.NewHarness(t, mcpserver.New())

	discovery := h.CallTool(mcpserver.ToolDiscoveryExecute, map[string]any{
		"code": "return discover.find({ task: 'post an escalation' })",
	})
	toolboxID := mcptest.StructuredMap(t, discovery)["toolboxID"]

	action := h.CallTool(mcpserver.ToolActionExecute, map[string]any{
		"toolboxID": toolboxID,
		"code":      "return await tools.slack.channels.messages.send({ channel_id: '#escalation', message: 'Urgent ticket' })",
	})

	if action.IsError {
		t.Fatalf("expected non-error result")
	}

	structured := mcptest.StructuredMap(t, action)
	if got := structured["mode"]; got != "action" {
		t.Fatalf("expected mode action, got %#v", got)
	}
	if got := structured["toolboxID"]; got != toolboxID {
		t.Fatalf("expected toolboxID %#v, got %#v", toolboxID, got)
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
