package quickts_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestRunCalcAddStub(t *testing.T) {
	tool := tooltest.CalcResolvedTool(t, "calc.add")
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

func TestRunProvidesBtoaAndAtobGlobals(t *testing.T) {
	def := inlineToolDef(t, "tools/base64.ts", `export default async function tool(_args: Record<string, never>, _ctx: unknown) {
  const encoded = btoa("hello world");
  const decoded = atob(encoded);
  return JSON.stringify({ encoded, decoded });
}
`)
	got, err := quickts.Run(def, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := `{"encoded":"aGVsbG8gd29ybGQ=","decoded":"hello world"}`
	if got != want {
		t.Fatalf("expected %s, got %q", want, got)
	}
}

func TestRunBtoaAtobNoLongerRequiresFixtureShim(t *testing.T) {
	def := inlineToolDef(t, "tools/no-shim.ts", `export default async function tool(_args: Record<string, never>, _ctx: unknown) {
  return atob("dG9vbGJveA==") + ":" + btoa("sandbox");
}
`)
	got, err := quickts.Run(def, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "toolbox:c2FuZGJveA==" {
		t.Fatalf("expected toolbox:c2FuZGJveA==, got %q", got)
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
	tool := tooltest.CalcResolvedTool(t, "calc.asyncAdd")
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
	tool := tooltest.CalcResolvedTool(t, "calc.asyncAdd")
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
	tool := tooltest.CalcResolvedTool(t, "calc.add")
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

func inlineToolDef(t *testing.T, entry, source string) tooldef.TSToolDef {
	t.Helper()
	files := fstest.MapFS{
		entry: &fstest.MapFile{Data: []byte(source), Mode: fs.FileMode(0o644)},
	}
	return tooldef.TSToolDef{
		Entry:       entry,
		Files:       files,
		PackageRoot: "/",
	}
}
