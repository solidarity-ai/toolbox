package toolset

import (
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// TestResourceBindingTwoTierFlow tests the full two-tier binding model:
// 1. Package declares resource params with canonical binding names
// 2. Toolset-level Config.ResourceBindings maps canonical names to CEL bindings
// 3. Resolve propagates resource bindings to matching tools
// 4. AgentView hides resource params, ValidateCall injects them
func TestResourceBindingTwoTierFlow(t *testing.T) {
	t.Parallel()

	tools := []tooldef.ResolvedTool{
		{
			Name:        "account.tickets.list",
			Description: "List tickets",
			ParamsSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account_id": map[string]any{"type": "string"},
					"status":     map[string]any{"type": "string"},
				},
				"required": []any{"account_id"},
			},
			ResourceParams: []tooldef.ResourceParam{
				{Name: "account_id", BindingName: "zendesk_account"},
			},
		},
		{
			Name:        "account.tickets.get",
			Description: "Get a ticket",
			ParamsSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account_id": map[string]any{"type": "string"},
					"ticket_id":  map[string]any{"type": "string"},
				},
				"required": []any{"account_id", "ticket_id"},
			},
			ResourceParams: []tooldef.ResourceParam{
				{Name: "account_id", BindingName: "zendesk_account"},
				{Name: "ticket_id", BindingName: "ticket_id"},
			},
		},
	}

	resolved, err := ResolveTools(tools, Config{
		ResourceBindings: map[string]Binding{
			"zendesk_account": {Value: "context.customer_id", Hidden: true},
		},
		Context: map[string]any{"customer_id": "cust_123"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// AgentView should hide account_id on both tools.
	view := resolved.AgentView()

	listTool := findAgentTool(t, view, "account.tickets.list")
	listProps := schemaProperties(t, listTool.ParamsSchema)
	if _, ok := listProps["account_id"]; ok {
		t.Error("account_id should be hidden from list tool AgentView")
	}
	if _, ok := listProps["status"]; !ok {
		t.Error("status should be visible in list tool AgentView")
	}

	getTool := findAgentTool(t, view, "account.tickets.get")
	getProps := schemaProperties(t, getTool.ParamsSchema)
	if _, ok := getProps["account_id"]; ok {
		t.Error("account_id should be hidden from get tool AgentView")
	}
	if _, ok := getProps["ticket_id"]; !ok {
		t.Error("ticket_id should be visible (no resource binding for ticket_id)")
	}

	// ValidateCall should inject account_id from context.
	params, err := resolved.ValidateCall("account.tickets.list", map[string]any{"status": "open"})
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}
	if params["account_id"] != "cust_123" {
		t.Errorf("expected account_id=cust_123, got %v", params["account_id"])
	}
	if params["status"] != "open" {
		t.Errorf("expected status=open, got %v", params["status"])
	}
}
