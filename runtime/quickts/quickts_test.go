package quickts_test

import (
	"context"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestRunCalcAddStub(t *testing.T) {
	tool := calcTool(t, "calc.add")
	got, err := quickts.Run(*tool.TS, map[string]any{
		"a": 7,
		"b": 4,
	}, tool.Sig)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "11" {
		t.Fatalf("expected 11, got %q", got)
	}
}

func TestRunnerSourceLegacy(t *testing.T) {
	got := quickts.RunnerSourceForTest(
		"tools/calc.add.ts",
		map[string]any{"a": 7, "b": 4},
		nil,
	)

	want := "import tool from \"./tools/calc.add.ts\";\n" +
		"const __r = await tool({\"a\":7,\"b\":4}, {}); export default typeof __r === \"string\" ? __r : JSON.stringify(__r);\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestAsyncReturnTypeUnwrapsPromise(t *testing.T) {
	tool := calcTool(t, "calc.asyncAdd")
	if tool.Sig == nil {
		t.Fatal("expected Sig to be populated for asyncAdd")
	}
	ret := tool.Sig.Return()
	if ret == nil {
		t.Fatal("expected Return() to be non-nil for async function")
	}
	got := ret.UnwrapPromise().ToTS()
	if got != "number" {
		t.Fatalf("expected Return().UnwrapPromise().ToTS() to be %q, got %q", "number", got)
	}
}

func TestRunCalcAsyncAddStub(t *testing.T) {
	tool := calcTool(t, "calc.asyncAdd")
	got, err := quickts.Run(*tool.TS, map[string]any{
		"a": 7,
		"b": 4,
	}, tool.Sig)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "11" {
		t.Fatalf("expected 11, got %q", got)
	}
}

func TestRunnerSourceSpreadsParams(t *testing.T) {
	tool := calcTool(t, "calc.add")
	got := quickts.RunnerSourceForTest(
		"tools/calc.add.ts",
		map[string]any{"a": 7, "b": 4},
		tool.Sig,
	)

	want := "import tool from \"./tools/calc.add.ts\";\n" +
		"const __r = await tool((7 satisfies Parameters<typeof tool>[0]), (4 satisfies Parameters<typeof tool>[1]));\n" +
		"export default typeof __r === \"string\" ? __r : JSON.stringify(__r);\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunnerSourceExcludesAccountParams(t *testing.T) {
	// The runner uses the original Sig from prepared.Tools(), not the
	// AgentView-augmented Sig. Verify that the runner source only includes
	// the original function params (a, b) and NOT any account params.
	tool := calcTool(t, "calc.add")

	// The original Sig should not have account params.
	for _, p := range tool.Sig.Params() {
		if strings.HasSuffix(p.Name(), "_account") {
			t.Fatalf("original Sig should not have account params, found %q", p.Name())
		}
	}

	// Generate runner source with an extra account param in args — it should
	// be ignored because the Sig only has (a, b).
	got := quickts.RunnerSourceForTest(
		"tools/calc.add.ts",
		map[string]any{"a": 7, "b": 4, "google_workspace_account": "admin@acme.com"},
		tool.Sig,
	)

	// The runner should only spread a and b, not google_workspace_account.
	if strings.Contains(got, "google_workspace_account") {
		t.Fatalf("runner source should not include account params, got:\n%s", got)
	}
	if strings.Contains(got, "admin@acme.com") {
		t.Fatalf("runner source should not include account param values, got:\n%s", got)
	}

	// Should still contain the original params.
	want := "import tool from \"./tools/calc.add.ts\";\n" +
		"const __r = await tool((7 satisfies Parameters<typeof tool>[0]), (4 satisfies Parameters<typeof tool>[1]));\n" +
		"export default typeof __r === \"string\" ? __r : JSON.stringify(__r);\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func calcTool(t testing.TB, name string) assembler.LoadedTool {
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
