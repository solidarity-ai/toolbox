package quickts_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestRunCalcAddStub(t *testing.T) {
	tool := calcTool(t, "calc.add")
	gotWire, err := quickts.Run(*tool.TS, tooltest.WireArgs(t, map[string]any{
		"a": 7,
		"b": 4,
	}), tool.Sig)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != "11" {
		t.Fatalf("expected 11, got %q", got)
	}
}

func TestRunRejectsWireArgTypeMismatch(t *testing.T) {
	tool := calcTool(t, "calc.add")
	args, err := jswire.FromAnonJSObj([]byte(`{"a":"6","b":3}`))
	if err != nil {
		t.Fatalf("FromAnonJSObj() error = %v", err)
	}

	_, err = quickts.Run(*tool.TS, args, tool.Sig)
	if err == nil {
		t.Fatal("expected typecheck error")
	}
	if !strings.Contains(err.Error(), "typescript check failed") {
		t.Fatalf("error = %v, want typecheck failure", err)
	}
}

func TestRunnerSourceLegacy(t *testing.T) {
	got := quickts.RunnerSourceForTest(
		"tools/calc.add.ts",
		map[string]any{"a": 7, "b": 4},
		nil,
	)

	want := "import tool from \"./tools/calc.add.ts\";\n" +
		"const __r = await tool((globalThis as any).__toolboxArgs ?? {}, {}); export default __r;\n"
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
	gotWire, err := quickts.Run(*tool.TS, tooltest.WireArgs(t, map[string]any{
		"a": 7,
		"b": 4,
	}), tool.Sig)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != "11" {
		t.Fatalf("expected 11, got %q", got)
	}
}

func TestRunApprovalPresentationUsesToolSignature(t *testing.T) {
	source := `
type GmailSendInput = {
  to: string;
  subject: string;
  body: string;
};

export default async function tool(input: GmailSendInput) {
  return input.to;
}

export function displayApproval(input: GmailSendInput) {
  return {
    schema: "toolbox.approval.presentation.v1",
    blocks: [
      {
        type: "fields",
        fields: [
          { label: "To", value: input.to },
          { label: "Subject", value: input.subject },
          { label: "Body", value: input.body, multiline: true },
        ],
      },
    ],
  };
}
`
	def := tooldef.TSToolDef{
		Entry: "tools/gmail.send.ts",
		Files: fstest.MapFS{
			"tools/gmail.send.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
	got, err := quickts.RunApprovalPresentation(context.Background(), def, map[string]any{
		"input": map[string]any{
			"to":      "sarah@example.com",
			"subject": "Follow-up from today",
			"body":    "Hi Sarah",
		},
	}, nil, tooltest.NewTSSig(t, source))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var presentation struct {
		Blocks []struct {
			Fields []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(got), &presentation); err != nil {
		t.Fatalf("approval presentation was not JSON: %v\n%s", err, got)
	}
	if len(presentation.Blocks) != 1 || len(presentation.Blocks[0].Fields) < 2 {
		t.Fatalf("unexpected approval presentation: %s", got)
	}
	if presentation.Blocks[0].Fields[0].Value != "sarah@example.com" {
		t.Fatalf("displayApproval received wrong input shape: %s", got)
	}
	if presentation.Blocks[0].Fields[1].Value != "Follow-up from today" {
		t.Fatalf("displayApproval received wrong subject: %s", got)
	}
}

func TestRunApprovalPresentationSpreadsMultipleToolParams(t *testing.T) {
	source := `
export default function tool(a: number, b: string) {
  return String(a) + ":" + b;
}

export function displayApproval(a: number, b: string) {
  return {
    schema: "toolbox.approval.presentation.v1",
    blocks: [
      { type: "text", text: String(a) + ":" + b },
    ],
  };
}
`
	def := tooldef.TSToolDef{
		Entry: "tools/multi.ts",
		Files: fstest.MapFS{
			"tools/multi.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
	got, err := quickts.RunApprovalPresentation(context.Background(), def, map[string]any{
		"a": 7,
		"b": "four",
	}, nil, tooltest.NewTSSig(t, source))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(got, `"text":"7:four"`) {
		t.Fatalf("displayApproval did not receive spread params: %s", got)
	}
}

func TestRunApprovalPresentationCanReadInjectedAccountArgument(t *testing.T) {
	source := `
type GmailSendInput = {
  to: string;
};

export default function tool(input: GmailSendInput) {
  return input.to;
}

export function displayApproval(input: GmailSendInput) {
  const account = (globalThis as any).__toolboxApprovalArgs?.workspace_account || "";
  return {
    schema: "toolbox.approval.presentation.v1",
    title: account ? "Send email (" + account + ")" : "Send email",
    blocks: [
      { type: "fields", fields: [{ label: "To", value: input.to }] },
    ],
  };
}
`
	def := tooldef.TSToolDef{
		Entry: "tools/gmail.send.ts",
		Files: fstest.MapFS{
			"tools/gmail.send.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
	got, err := quickts.RunApprovalPresentation(context.Background(), def, map[string]any{
		"input": map[string]any{
			"to": "sarah@example.com",
		},
		"workspace_account": "work@example.com",
	}, nil, tooltest.NewTSSig(t, source))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var presentation struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(got), &presentation); err != nil {
		t.Fatalf("approval presentation was not JSON: %v\n%s", err, got)
	}
	if presentation.Title != "Send email (work@example.com)" {
		t.Fatalf("title = %q, want account-aware title; raw=%s", presentation.Title, got)
	}
}

func TestRunApprovalPresentationMissingExportFallsBackEmpty(t *testing.T) {
	source := `
export default function tool(a: number, b: string) {
  return String(a) + ":" + b;
}
`
	def := tooldef.TSToolDef{
		Entry: "tools/no-approval.ts",
		Files: fstest.MapFS{
			"tools/no-approval.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
	got, err := quickts.RunApprovalPresentation(context.Background(), def, map[string]any{
		"a": 7,
		"b": "four",
	}, nil, tooltest.NewTSSig(t, source))
	if err != nil {
		t.Fatalf("expected missing displayApproval to fall back without TS error, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty fallback, got %q", got)
	}
}

func TestRunApprovalPresentationRejectsMismatchedDisplayApprovalParams(t *testing.T) {
	source := `
export default function tool(a: number, b: string) {
  return String(a) + ":" + b;
}

export function displayApproval(a: string, b: string) {
  return {
    schema: "toolbox.approval.presentation.v1",
    blocks: [
      { type: "text", text: a + ":" + b },
    ],
  };
}
`
	def := tooldef.TSToolDef{
		Entry: "tools/mismatch.ts",
		Files: fstest.MapFS{
			"tools/mismatch.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
	_, err := quickts.RunApprovalPresentation(context.Background(), def, map[string]any{
		"a": 7,
		"b": "four",
	}, nil, tooltest.NewTSSig(t, source))
	if err == nil {
		t.Fatal("expected mismatched displayApproval params to fail TypeScript check")
	}
	if !strings.Contains(err.Error(), "not assignable to type 'never'") {
		t.Fatalf("expected displayApproval parameter check failure, got %v", err)
	}
}

func TestApprovalPresentationRunnerSourceChecksDisplayApprovalParams(t *testing.T) {
	tool := calcTool(t, "calc.add")
	got := quickts.ApprovalPresentationRunnerSourceForTest(
		"tools/calc.add.ts",
		map[string]any{"a": 7, "b": 4},
		tool.Sig,
	)

	for _, want := range []string{
		`const __toolboxApprovalArgs = {"a":7,"b":4};`,
		"(globalThis as any).__toolboxApprovalArgs = __toolboxApprovalArgs;",
		"type __ToolboxDisplayApprovalParams = typeof mod extends { displayApproval: (...args: infer P) => any } ? P : Parameters<typeof tool>;",
		"const __toolboxDisplayApprovalParamsCheck: __ToolboxExactParams<__ToolboxDisplayApprovalParams, Parameters<typeof tool>> = true;",
		"await __displayApproval((7 satisfies Parameters<typeof tool>[0]), (4 satisfies Parameters<typeof tool>[1]))",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("approval runner source missing %q:\n%s", want, got)
		}
	}
}

func TestRunContextTimeoutDoesNotPoisonLaterRun(t *testing.T) {
	slow := tooldef.TSToolDef{
		Entry: "tools/slow.ts",
		Files: fstest.MapFS{
			"tools/slow.ts": &fstest.MapFile{Data: []byte(`
export default async function tool() {
  await new Promise(() => {});
  return "done";
}
`)},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := quickts.RunContext(ctx, slow, tooltest.WireArgs(t, map[string]any{}), nil); err == nil {
		t.Fatal("expected timed out quickts run to fail")
	}

	tool := calcTool(t, "calc.add")
	gotWire, err := quickts.RunContext(context.Background(), *tool.TS, tooltest.WireArgs(t, map[string]any{
		"a": 2,
		"b": 3,
	}), tool.Sig)
	if err != nil {
		t.Fatalf("expected later quickts run to recover, got %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != "5" {
		t.Fatalf("expected 5, got %q", got)
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
		"const __toolboxArgs = ((globalThis as any).__toolboxArgs ?? {}) as { \"a\": number; \"b\": number };\n" +
		"const __r = await tool((__toolboxArgs[\"a\"] satisfies Parameters<typeof tool>[0]), (__toolboxArgs[\"b\"] satisfies Parameters<typeof tool>[1]));\n" +
		"export default __r;\n"
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
		"const __toolboxArgs = ((globalThis as any).__toolboxArgs ?? {}) as { \"a\": number; \"b\": number };\n" +
		"const __r = await tool((__toolboxArgs[\"a\"] satisfies Parameters<typeof tool>[0]), (__toolboxArgs[\"b\"] satisfies Parameters<typeof tool>[1]));\n" +
		"export default __r;\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunInstallsBrowserCompat(t *testing.T) {
	gotWire, err := quickts.RunWithHost(tooldef.TSToolDef{
		Entry: "tools/browser-compat.ts",
		Files: fstest.MapFS{
			"tools/browser-compat.ts": &fstest.MapFile{Data: []byte(`
export default function tool(_args?: any, _ctx?: any) {
  const bytes = new Uint8Array(4);
  return JSON.stringify({
    hasNavigatorUserAgent: typeof (globalThis as any).navigator.userAgent === "string" && (globalThis as any).navigator.userAgent.length > 0,
    textRoundTrip: new TextDecoder().decode(new TextEncoder().encode("A€😀")),
    btoa: (globalThis as any).btoa("Man"),
    atob: (globalThis as any).atob("TWE="),
    bytes: Array.from((globalThis as any).crypto.getRandomValues(bytes)),
    uuid: (globalThis as any).crypto.randomUUID(),
  });
}
`)},
		},
	}, tooltest.WireArgs(t, map[string]any{}), quickts.Host{
		RandomBytes: func(n int) ([]byte, error) {
			out := make([]byte, n)
			for i := range out {
				out[i] = byte(i)
			}
			return out, nil
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != `{"hasNavigatorUserAgent":true,"textRoundTrip":"A€😀","btoa":"TWFu","atob":"Ma","bytes":[0,1,2,3],"uuid":"00010203-0405-4607-8809-0a0b0c0d0e0f"}` {
		t.Fatalf("unexpected browser compat result: %q", got)
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
