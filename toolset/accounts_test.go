package toolset_test

import (
	"context"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestResolveWithCredentialAccounts_ParamCollision(t *testing.T) {
	t.Parallel()

	rt := assembler.LoadedTool{
		Name:        "mypackage.list",
		Description: "Test tool mypackage.list",
		Sig:         tooltest.NewTSSig(t, `export default function(google_workspace_account: string, query: string) {}`),
		PackageMeta: &tooldef.Package{
			Module:  tooldef.ModulePath("example.com/mypackage"),
			Name:    "mypackage",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
		},
	}

	_, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{rt}, toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			rt.PackageMeta.Module: {
				CredentialAccounts: map[string][]string{
					"google_workspace": {"admin@acme.com", "personal@gmail.com"},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for param collision")
	}
	if !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected 'collides' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "google_workspace_account") {
		t.Fatalf("expected param name in error, got: %v", err)
	}
}

func TestAccountParamsInFuncSignature(t *testing.T) {
	t.Parallel()

	// Use the calc fixture which has tools with Sig (type-checked TS).
	prepared := calcToolset(t, toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			tooldef.ModulePath("fixtures.local/calc"): {
				CredentialAccounts: map[string][]string{
					"google_workspace": {"admin@acme.com", "personal@gmail.com"},
				},
			},
		},
	})

	view := prepared.AgentView()
	tool := findAgentTool(t, view, "calc.add")

	// The AgentTool Sig should have the account param added.
	if tool.Sig == nil {
		t.Fatal("expected Sig to be non-nil")
	}

	params := tool.Sig.Params()
	// Original calc.add has params (a, b). With account injection it should have (a, b, google_workspace_account).
	found := false
	for _, p := range params {
		if p.Name() == "google_workspace_account" {
			found = true
			// The type should be a string union.
			schema := p.Type().ToJSONSchema()
			enumVals, ok := schema["enum"].([]any)
			if !ok {
				t.Fatalf("expected enum in account param type, got %v", schema)
			}
			if len(enumVals) != 2 {
				t.Fatalf("expected 2 enum values, got %d", len(enumVals))
			}
			if enumVals[0] != "admin@acme.com" || enumVals[1] != "personal@gmail.com" {
				t.Errorf("unexpected enum values: %v", enumVals)
			}
			if p.Optional() {
				t.Error("account param should be required")
			}
			break
		}
	}
	if !found {
		t.Fatal("expected google_workspace_account param in Sig")
	}

	// ParamsType() should also include the account param with enum.
	pt := tool.ParamsType()
	if pt == nil {
		t.Fatal("expected non-nil ParamsType")
	}
	schema := pt.ToJSONSchema()
	propsMap, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties in schema, got %v", schema)
	}
	acctProp, ok := propsMap["google_workspace_account"].(map[string]any)
	if !ok {
		t.Fatal("expected google_workspace_account in ParamsType schema")
	}
	enumVals, ok := acctProp["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum in account property, got %v", acctProp)
	}
	if len(enumVals) != 2 {
		t.Fatalf("expected 2 enum values, got %d", len(enumVals))
	}

	// ParamsSchema should also have the account param (derived from Sig).
	props, ok := tool.ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in ParamsSchema")
	}
	if _, ok := props["google_workspace_account"]; !ok {
		t.Fatal("expected google_workspace_account in ParamsSchema")
	}
}

func TestAccountParamsInFuncSignature_BackwardCompat(t *testing.T) {
	t.Parallel()

	// Tool with Sig, no credential accounts — Sig should be unchanged.
	prepared := calcToolset(t, toolset.Config{})

	view := prepared.AgentView()
	tool := findAgentTool(t, view, "calc.add")

	if tool.Sig == nil {
		t.Fatal("expected Sig to be non-nil")
	}

	// Should have exactly the original params (a, b) — no account params injected.
	params := tool.Sig.Params()
	for _, p := range params {
		if strings.HasSuffix(p.Name(), "_account") {
			t.Fatalf("unexpected account param %q in Sig without credentials", p.Name())
		}
	}
}
