package service_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/service"
	"github.com/solidarity-ai/toolbox/testutil/servicetest"
)

func TestExecuteToolDiscovery(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		code string
	}{
		{
			name: "stub discovery returns toolbox id",
			env: map[string]string{
				"customer_id": "acme-123",
			},
			code: "return discover.find({ task: 'triage zendesk tickets' })",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := servicetest.ExecuteToolDiscovery(t, tt.env, service.ToolDiscoveryExecuteRequest{
				Code: tt.code,
			})

			if result.IsError {
				t.Fatalf("expected non-error result")
			}

			structured := servicetest.StructuredMap(t, result)
			if got := structured["mode"]; got != "discovery" {
				t.Fatalf("expected mode discovery, got %#v", got)
			}
			if got := structured["toolboxID"]; got != "tbx_test_snapshot" {
				t.Fatalf("expected toolboxID tbx_test_snapshot, got %#v", got)
			}
		})
	}
}

func TestExecuteToolAction(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		toolboxID string
		code      string
	}{
		{
			name: "stub action echoes toolbox id",
			env: map[string]string{
				"customer_id": "acme-123",
			},
			toolboxID: "tbx_test_snapshot",
			code:      "return await tools.slack.channels.messages.send({ channel_id: '#escalation', message: 'Urgent ticket' })",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := servicetest.ExecuteToolAction(t, tt.env, service.ToolActionExecuteRequest{
				ToolboxID: tt.toolboxID,
				Code:      tt.code,
			})

			if result.IsError {
				t.Fatalf("expected non-error result")
			}

			structured := servicetest.StructuredMap(t, result)
			if got := structured["mode"]; got != "action" {
				t.Fatalf("expected mode action, got %#v", got)
			}
			if got := structured["toolboxID"]; got != tt.toolboxID {
				t.Fatalf("expected toolboxID %#v, got %#v", tt.toolboxID, got)
			}
		})
	}
}
