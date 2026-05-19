package toolset_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestAgentViewNoBindingsShowsAllParams(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{})
	view := prepared.AgentView()

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

func TestAgentViewReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{
		EnvContext: map[string]any{
			"fixed_a": 42,
			"fixed_b": 7,
		},
		Tools: []toolset.BoundTool{
			{
				ToolRef: "calc.add",
				Bindings: map[string]toolset.Binding{
					"a": {Value: "context.fixed_a", Hidden: true},
					"b": {Value: "context.fixed_b"},
				},
			},
		},
	})

	view := prepared.AgentView()
	addIdx := findAgentToolIndex(t, view, "calc.add")
	addTool := view.Tools[addIdx]
	view.Tools[addIdx].Name = "mutated"
	addTool.ParamsSchema["x-mutated"] = true
	props := addTool.ParamsSchema["properties"].(map[string]any)
	props["mutated"] = map[string]any{"type": "string"}
	addTool.HiddenParams()["mutated"] = true
	addTool.BoundLiterals()["mutated"] = true

	fresh := prepared.AgentView()
	if hasAgentTool(fresh, "mutated") {
		t.Fatal("AgentView tool slice mutation leaked into cached view")
	}

	freshTool := findAgentTool(t, fresh, "calc.add")
	if _, ok := freshTool.ParamsSchema["x-mutated"]; ok {
		t.Fatal("AgentView top-level ParamsSchema mutation leaked into cached view")
	}
	freshProps := freshTool.ParamsSchema["properties"].(map[string]any)
	if _, ok := freshProps["mutated"]; ok {
		t.Fatal("AgentView nested ParamsSchema mutation leaked into cached view")
	}
	if hidden := freshTool.HiddenParams(); hidden["mutated"] {
		t.Fatal("AgentView hidden params mutation leaked into cached view")
	}
	if _, ok := freshTool.BoundLiterals()["mutated"]; ok {
		t.Fatal("AgentView bound literals mutation leaked into cached view")
	}
}

func TestAgentViewHiddenParamRemovedFromSchema(t *testing.T) {
	t.Parallel()

	cfg := toolset.Config{
		EnvContext: map[string]any{
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

	prepared := calcToolset(t, cfg)
	view := prepared.AgentView()

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
		EnvContext: map[string]any{
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

	// Should prepare without error — check expressions compile at prepare time
	prepared := calcToolset(t, cfg)
	view := prepared.AgentView()

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

	_, err := calcPrepare(t, cfg)
	if err == nil {
		t.Fatal("expected error for invalid CEL expression")
	}
}

func TestAgentViewEffectAndIdempotent(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{})
	view := prepared.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	if addTool.Effect != tooldef.EffectReadOnly {
		t.Fatalf("expected calc.add effect=readOnly, got %q", addTool.Effect)
	}
	if addTool.Idempotent == nil || !*addTool.Idempotent {
		t.Fatal("expected calc.add idempotent=true")
	}
}

func TestAgentViewKeepsUnavailableToolsVisible(t *testing.T) {
	t.Parallel()

	prepared := calcToolset(t, toolset.Config{
		CredentialPolicySource: lockedModulePolicySource{
			tooldef.ModulePath("fixtures.local/calc"): secrets.ErrLocked,
		},
	})
	view := prepared.AgentView()

	addTool := findAgentTool(t, view, "calc.add")
	if addTool.UnavailableReason != toolset.ToolUnavailableReasonSecretStoreLocked {
		t.Fatalf("UnavailableReason = %q, want %q", addTool.UnavailableReason, toolset.ToolUnavailableReasonSecretStoreLocked)
	}
	if addTool.ParamsSchema == nil {
		t.Fatal("expected ParamsSchema for unavailable calc.add")
	}
	if !strings.Contains(addTool.Description, "Currently unavailable because the toolbox secret store is locked.") {
		t.Fatalf("Description = %q, want unavailable note", addTool.Description)
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

	preparedTool, ok := prepared.Tool("calc.add")
	if !ok {
		t.Fatal("expected prepared calc.add")
	}
	var unavailable *toolset.ToolUnavailableError
	if _, err := preparedTool.ValidateCall(map[string]any{"a": 1, "b": 2}); !errors.As(err, &unavailable) {
		t.Fatalf("ValidateCall() error = %v, want ToolUnavailableError", err)
	}
}

func calcToolset(t testing.TB, cfg toolset.Config) toolset.PreparedToolset {
	t.Helper()
	prepared, err := calcPrepare(t, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prepared
}

func calcPrepare(t testing.TB, cfg toolset.Config) (toolset.PreparedToolset, error) {
	t.Helper()

	loaded, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("calc"))
	if err != nil {
		t.Fatalf("load calc fixture: %v", err)
	}
	return toolset.PrepareTools(context.Background(), loaded.Tools(), cfg)
}

func calcLoadedTool(t testing.TB, name string) assembler.LoadedTool {
	t.Helper()

	loaded, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("calc"))
	if err != nil {
		t.Fatalf("load calc fixture: %v", err)
	}
	pkg, ok := loaded.Package("calc")
	if !ok {
		t.Fatal("calc package not found")
	}
	tool, ok := pkg.Tool(name)
	if !ok {
		t.Fatalf("tool %q not found", name)
	}
	return tool
}

func findAgentTool(t testing.TB, view toolset.AgentView, name string) toolset.AgentTool {
	t.Helper()
	if idx := findAgentToolIndex(t, view, name); idx >= 0 {
		return view.Tools[idx]
	}
	t.Fatalf("tool %q not found in AgentView", name)
	return toolset.AgentTool{}
}

func findAgentToolIndex(t testing.TB, view toolset.AgentView, name string) int {
	t.Helper()
	for i, tool := range view.Tools {
		if tool.Name == name {
			return i
		}
	}
	t.Fatalf("tool %q not found in AgentView", name)
	return -1
}

func hasAgentTool(view toolset.AgentView, name string) bool {
	for _, tool := range view.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

type lockedModulePolicySource map[tooldef.ModulePath]error

func (s lockedModulePolicySource) PackageCredentialPolicy(_ context.Context, pkg tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	if err, ok := s[pkg.Module]; ok {
		return toolset.PackageCredentialPolicy{}, err
	}
	return toolset.PackageCredentialPolicy{}, nil
}

func TestAgentViewResourceBindingHidesParam(t *testing.T) {
	t.Parallel()

	// Use the real calc.add fixture (has params a, b with a Sig).
	// Add a ResourceParam so we can verify that without bindings all params
	// remain visible.
	rt := calcLoadedTool(t, "calc.add")
	rt.ResourceParams = []tooldef.ResourceParam{
		{Name: "a", BindingName: "shared_a"},
	}
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{rt})

	view := prepared.AgentView()
	tool := findAgentTool(t, view, "calc.add")

	// Without bindings, both params should be visible
	props := tool.ParamsSchema["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatal("expected param 'a' in unbound view")
	}
	if _, ok := props["b"]; !ok {
		t.Fatal("expected param 'b' in unbound view")
	}
}

// TestResourceBindingTwoTierFlow tests the full two-tier binding model:
// 1. Package declares resource params with canonical binding names
// 2. Toolset-level Config.ResourceBindings maps canonical names to CEL bindings
// 3. Prepare propagates resource bindings to matching tools
// 4. AgentView hides resource params, ValidateCall injects them
func TestResourceBindingTwoTierFlow(t *testing.T) {
	t.Parallel()

	// Use real calc fixtures (calc.add and calc.sub both have params a, b).
	// Treat param "a" as a shared resource param on both tools.
	addTool := calcLoadedTool(t, "calc.add")
	addTool.ResourceParams = []tooldef.ResourceParam{
		{Name: "a", BindingName: "shared_a"},
	}

	subTool := calcLoadedTool(t, "calc.sub")
	subTool.ResourceParams = []tooldef.ResourceParam{
		{Name: "a", BindingName: "shared_a"},
		{Name: "b", BindingName: "shared_b"},
	}

	tools := []assembler.LoadedTool{addTool, subTool}

	prepared, err := toolset.PrepareTools(context.Background(), tools, toolset.Config{
		ResourceBindings: map[string]toolset.Binding{
			"shared_a": {Value: "context.fixed_a", Hidden: true},
		},
		EnvContext: map[string]any{"fixed_a": 42},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// AgentView should hide param "a" on both tools (bound via shared_a).
	view := prepared.AgentView()

	addAgent := findAgentTool(t, view, "calc.add")
	addProps := addAgent.ParamsSchema["properties"].(map[string]any)
	if _, ok := addProps["a"]; ok {
		t.Error("param 'a' should be hidden from calc.add AgentView")
	}
	if _, ok := addProps["b"]; !ok {
		t.Error("param 'b' should be visible in calc.add AgentView")
	}

	subAgent := findAgentTool(t, view, "calc.sub")
	subProps := subAgent.ParamsSchema["properties"].(map[string]any)
	if _, ok := subProps["a"]; ok {
		t.Error("param 'a' should be hidden from calc.sub AgentView")
	}
	if _, ok := subProps["b"]; !ok {
		t.Error("param 'b' should be visible (no resource binding for shared_b)")
	}

	// ValidateCall should inject param "a" from context.
	params, err := prepared.ValidateCall("calc.add", map[string]any{"b": 10})
	if err != nil {
		t.Fatalf("ValidateCall: %v", err)
	}
	// CEL evaluates integer context values as int64.
	if params["a"] != int64(42) {
		t.Errorf("expected a=42, got %v (type %T)", params["a"], params["a"])
	}
	if params["b"] != 10 {
		t.Errorf("expected b=10, got %v", params["b"])
	}
}

func TestAgentViewConcurrency(t *testing.T) {
	prepared := calcToolset(t, toolset.Config{})

	const numGoroutines = 20
	const iterations = 50

	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				view := prepared.AgentView()
				if len(view.Tools) == 0 {
					errChan <- errors.New("expected tools in view")
					return
				}
				for _, tool := range view.Tools {
					_ = tool.ParamsType()
				}
			}
			errChan <- nil
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		if err := <-errChan; err != nil {
			t.Fatal(err)
		}
	}
}
