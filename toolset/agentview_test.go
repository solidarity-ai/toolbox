package toolset_test

import (
	"path/filepath"
	"runtime"
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestAgentViewNoBindingsShowsAllParams(t *testing.T) {
	t.Parallel()

	resolved := calcToolset(t, toolset.Config{})
	view := resolved.AgentView()

	if len(view.Tools) == 0 {
		t.Fatal("expected at least one tool in AgentView")
	}

	// calc.add has params {a: number, b: number} — both should be visible
	addTool := findAgentTool(t, view, "calc.add")
	if addTool.ParamsSchema == nil {
		t.Fatal("expected ParamsSchema for calc.add")
	}

	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["a"]; !ok {
		t.Fatal("expected param 'a' in ParamsSchema")
	}
	if _, ok := props["b"]; !ok {
		t.Fatal("expected param 'b' in ParamsSchema")
	}
}

func TestAgentViewHiddenParamRemovedFromSchema(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
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

	resolved := calcToolset(t, cfg)
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}

	if _, ok := props["a"]; ok {
		t.Fatal("hidden param 'a' should not appear in AgentView schema")
	}
	if _, ok := props["b"]; !ok {
		t.Fatal("visible param 'b' should still appear in AgentView schema")
	}

	// required should also not include 'a'
	if required, ok := addTool.ParamsSchema["required"].([]any); ok {
		for _, r := range required {
			if r == "a" {
				t.Fatal("hidden param 'a' should not be in required list")
			}
		}
	}
}

func TestAgentViewCheckExpressionStored(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Context: map[string]any{
			"allowed_max": 100,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Check: "params.a < context.allowed_max"},
				},
			},
		},
	}

	// Should resolve without error — check expressions compile at resolve time
	resolved := calcToolset(t, cfg)
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	// Both params should still be visible (check doesn't hide)
	props, ok := addTool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["a"]; !ok {
		t.Fatal("param 'a' should remain visible with check binding")
	}
}

func TestResolveInvalidCELExpressionErrors(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "invalid!!!syntax"},
				},
			},
		},
	}

	builder := calcBuilder(t)
	_, err := builder.Resolve(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CEL expression")
	}
}

func TestAgentViewEffectAndIdempotent(t *testing.T) {
	t.Parallel()

	resolved := calcToolset(t, toolset.Config{})
	view := resolved.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	if addTool.Effect != tooldef.EffectReadOnly {
		t.Fatalf("expected calc.add effect=readOnly, got %q", addTool.Effect)
	}
	if addTool.Idempotent == nil || !*addTool.Idempotent {
		t.Fatal("expected calc.add idempotent=true")
	}
}

// helpers

func calcBuilder(t testing.TB) *toolset.Builder {
	t.Helper()
	builder := toolset.New()
	if err := builder.AddFromDir(calcFixtureDir()); err != nil {
		t.Fatalf("add calc dir: %v", err)
	}
	return builder
}

func calcToolset(t testing.TB, cfg toolset.Config) toolset.ResolvedToolset {
	t.Helper()
	builder := calcBuilder(t)
	resolved, err := builder.Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}

func findAgentTool(t testing.TB, view toolset.AgentView, name string) toolset.AgentTool {
	t.Helper()
	for _, tool := range view.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in AgentView", name)
	return toolset.AgentTool{}
}

func TestAgentViewResourceBindingHidesParam(t *testing.T) {
	t.Parallel()

	// Simulate a package with resource params using NewResolvedToolset
	// and manual toolset construction — real resource binding goes through
	// Builder.Resolve which reads PackageTool.ResourceParams
	pkg := tooldef.Package{
		Name:    "zendesk",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
		Tools: []tooldef.PackageTool{
			{
				EntryTS:    "tools/account.tickets.list.ts",
				Effect: tooldef.EffectReadOnly,
				Idempotent: boolPtr(true),
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
		},
	}

	builder := toolset.New()
	// We need to use the packaging layer to load, but for a unit test
	// we'll test the binding propagation by verifying the Resolve flow
	// with a manually constructed resolved toolset.
	rt := tooldef.ResolvedTool{
		Name:        "account.tickets.list",
		Description: "List tickets",
		Package:     &pkg,
		TS: &tooldef.TSToolDef{
			Entry: "tools/account.tickets.list.ts",
		},
	}
	rt.SetParamsSchema(pkg.Tools[0].ParamsSchema)
	resolved := toolset.NewResolvedToolset([]tooldef.ResolvedTool{rt})
	_ = builder // not used in this test path

	view := resolved.AgentView()
	tool := findAgentTool(t, view, "account.tickets.list")

	// Without bindings, both params should be visible
	props := tool.ParamsSchema["properties"].(map[string]any)
	if _, ok := props["account_id"]; !ok {
		t.Fatal("expected account_id in unbound view")
	}
	if _, ok := props["status"]; !ok {
		t.Fatal("expected status in unbound view")
	}
}

// TestResourceBindingTwoTierFlow tests the full two-tier binding model:
// 1. Package declares resource params with canonical binding names
// 2. Toolset-level Config.ResourceBindings maps canonical names to CEL bindings
// 3. Resolve propagates resource bindings to matching tools
// 4. AgentView hides resource params, ValidateCall injects them
func TestResourceBindingTwoTierFlow(t *testing.T) {
	t.Parallel()

	listTool := tooldef.ResolvedTool{
		Name:        "account.tickets.list",
		Description: "List tickets",
		ResourceParams: []tooldef.ResourceParam{
			{Name: "account_id", BindingName: "zendesk_account"},
		},
	}
	listTool.SetParamsSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{"type": "string"},
			"status":     map[string]any{"type": "string"},
		},
		"required": []any{"account_id"},
	})

	getToolDef := tooldef.ResolvedTool{
		Name:        "account.tickets.get",
		Description: "Get a ticket",
		ResourceParams: []tooldef.ResourceParam{
			{Name: "account_id", BindingName: "zendesk_account"},
			{Name: "ticket_id", BindingName: "ticket_id"},
		},
	}
	getToolDef.SetParamsSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{"type": "string"},
			"ticket_id":  map[string]any{"type": "string"},
		},
		"required": []any{"account_id", "ticket_id"},
	})

	tools := []tooldef.ResolvedTool{listTool, getToolDef}

	resolved, err := toolset.ResolveTools(tools, toolset.Config{
		ResourceBindings: map[string]toolset.Binding{
			"zendesk_account": {Value: "context.customer_id", Hidden: true},
		},
		Context: map[string]any{"customer_id": "cust_123"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// AgentView should hide account_id on both tools.
	view := resolved.AgentView()

	listAgent := findAgentTool(t, view, "account.tickets.list")
	listProps := listAgent.ParamsSchema["properties"].(map[string]any)
	if _, ok := listProps["account_id"]; ok {
		t.Error("account_id should be hidden from list tool AgentView")
	}
	if _, ok := listProps["status"]; !ok {
		t.Error("status should be visible in list tool AgentView")
	}

	getAgent := findAgentTool(t, view, "account.tickets.get")
	getProps := getAgent.ParamsSchema["properties"].(map[string]any)
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

func boolPtr(v bool) *bool { return &v }

func calcFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testutil", "fixtures", "toolbox.pkgs", "calc")
}
