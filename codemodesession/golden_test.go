package codemodesession_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

var (
	toolCallIDPattern   = regexp.MustCompile(`"toolCallId": "[^"]+"`)
	toolCallRefPattern  = regexp.MustCompile(`\$tool_call\("[^"]+"\)`)
	toolCallLinePattern = regexp.MustCompile(`(?m)^(\d+\. )"[^"]+"$`)
)

func TestSubmitOutputGoldens(t *testing.T) {
	t.Run("nested-resource-collections", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
			PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("resource-collections"), toolset.Config{}),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_nested_resource_collections.txt"), normalizeToolCallRefs(session.Submit(ctx, `const workbook = resource_collections.workbook("/tmp/book.xlsx");
const officejs = await workbook.officejs.run("return 1");
const sheets = await workbook.sheet.list();
const sheet = await workbook.sheet("Forecast").get();
const range = await workbook.sheet("Forecast").range("A1", "B2").get();
		({ officejs, sheets, sheet, range })`)))
	})

	t.Run("resource-selector-is-pure", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
			PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("resource-collections"), toolset.Config{}),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_resource_selector_is_pure.txt"), session.Submit(ctx, `const workbook = resource_collections.workbook("/tmp/book.xlsx");
const sheetSelector = workbook.sheet;
const selectedSheet = sheetSelector("Forecast");
({
  selectedHasGet: typeof selectedSheet.get === "function",
  selectedHasRange: typeof selectedSheet.range === "function",
  selectorHasList: typeof sheetSelector.list === "function",
  selectorStartedTool: "toolCallTask" in selectedSheet,
})`))
	})

	t.Run("bound-hidden-resource-selector-collapses", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
			PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("resource-collections"), toolset.Config{
				EnvContext: map[string]any{"workbook_path": "/tmp/bound.xlsx"},
				ResourceBindings: map[string]toolset.Binding{
					"workbook": {Value: "context.workbook_path", Hidden: true},
				},
			}),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_bound_hidden_resource_selector_collapses.txt"), session.Submit(ctx, `const workbook = resource_collections.workbook;
const officejs = await workbook.officejs.run("return 1");
const sheet = await workbook.sheet("Forecast").get();
({
  workbookIsObject: typeof workbook === "object",
  officejsRunIsMethod: typeof workbook.officejs.run === "function",
  sheetRemainsSelector: typeof workbook.sheet === "function",
  selectedSheetHasGet: typeof workbook.sheet("Forecast").get === "function",
  officejs,
  sheet,
})`))
	})

	t.Run("resource-intrinsic-collisions", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
			PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("resource-intrinsic-collisions"), toolset.Config{}),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_resource_intrinsic_collisions.txt"), session.Submit(ctx, `const selector = resource_intrinsic_collisions.workbook;
const selectedViaCall = selector._call(undefined, "/tmp/book.xlsx");
({
  naturalNameIsTool: typeof selector.name === "function",
  naturalLengthIsTool: typeof selector.length === "function",
  naturalPrototypeIsTool: typeof selector.prototype === "function",
  originalNameType: typeof selector._name(),
  originalLengthType: typeof selector._length(),
  originalPrototypeIsUndefined: selector._prototype() === undefined,
  intrinsicCallStillSelects: typeof (selectedViaCall as { get?: unknown }).get === "function",
})`))
	})

	t.Run("typecheck-failure", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_typecheck_failure.txt"), session.Submit(ctx, `const value: number = "x"`))
	})

	t.Run("typecheck-failure-long-type", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		api := strings.Repeat("declare namespace pkg { function get(id: number): Promise<Item>; } ", 8)
		out := session.Submit(ctx, `const meta = { api: "`+api+`" } as const; meta.tools`)
		if !strings.Contains(out, api) {
			t.Fatalf("Submit output truncates the offending type:\n%s", out)
		}
		checkGolden(t, goldenPath("submit_typecheck_failure_long_type.txt"), out)
	})

	t.Run("wrapped-object-literal-diagnostic", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_wrapped_object_literal_diagnostic.txt"), session.Submit(ctx, `{ a: missingVar }`))
	})

	t.Run("runtime-throw-with-logs", func(t *testing.T) {
		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_runtime_throw_with_logs.txt"), session.Submit(ctx, `console.log("bad", { a: 2 }); throw new Error("boom")`))
	})

	t.Run("timeout", func(t *testing.T) {
		originalTimeout := codemodesession.DefaultSubmitTimeout
		codemodesession.DefaultSubmitTimeout = 250 * time.Millisecond
		t.Cleanup(func() {
			codemodesession.DefaultSubmitTimeout = originalTimeout
		})

		ctx := context.Background()
		session, err := codemodesession.OpenMemory(ctx, t.TempDir())
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_timeout.txt"), session.Submit(ctx, `await new Promise(() => {})`))
	})

	t.Run("timeout-partial-tool-completion", func(t *testing.T) {
		originalTimeout := codemodesession.DefaultSubmitTimeout
		codemodesession.DefaultSubmitTimeout = time.Second
		t.Cleanup(func() {
			codemodesession.DefaultSubmitTimeout = originalTimeout
		})

		ctx := context.Background()
		tempDir := t.TempDir()
		dir := writeSessionPackage(t, filepath.Join(tempDir, "partial-timeout"), "example.com/partial-timeout", "partial_timeout", map[string]string{
			"tools/fast.ts": `export default async function tool(id: string): Promise<{ id: string; kind: string }> {
  return { id, kind: "fast" };
}
`,
			"tools/slow.ts": `export default async function tool(id: string): Promise<{ id: string; kind: string }> {
  return await new Promise(() => {});
}
`,
		})

		session, err := codemodesession.OpenMemory(ctx, tempDir, codemodesession.SessionConfig{
			PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{}),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		checkGolden(t, goldenPath("submit_timeout_partial_tool_completion.txt"), normalizeToolCallRefs(session.Submit(ctx, `const first = await partial_timeout.fast("I-1");
const second = await partial_timeout.fast("I-2");
const third = partial_timeout.slow("I-3");
await new Promise(() => {});
[first, second, third]`)))
	})

	t.Run("multi-tool-rejection", func(t *testing.T) {
		ctx := context.Background()
		tempDir := t.TempDir()
		dir := writeSessionPackage(t, filepath.Join(tempDir, "issues"), "example.com/issues", "issues", map[string]string{
			"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; kind: string }> {
  return { id, kind: "get" };
}
`,
			"tools/lookup.ts": `export default async function tool(id: string): Promise<{ id: string; kind: string }> {
  return { id, kind: "lookup" };
}
`,
		})

		session, err := codemodesession.OpenMemory(ctx, tempDir, codemodesession.SessionConfig{
			PreparedTools: prepareToolsetWithApprovals(t, dir, "issues.get", "issues.lookup"),
		})
		if err != nil {
			t.Fatalf("OpenMemory() error: %v", err)
		}
		defer session.Close()

		out := session.Submit(ctx, `var tasks = [issues.get("I-1"), issues.get("I-2"), issues.get("I-3"), issues.lookup("I-4")];
tasks.map((task) => $tool_call(task).status)`)
		assertContains(t, out, `"needsApproval"`)

		groups := mustPendingApprovalGroups(t, ctx, session)
		if len(groups) != 1 {
			t.Fatalf("pending approval groups len = %d, want 1", len(groups))
		}
		decisions := make([]codemodesession.ApprovalDecision, 0, len(groups[0].ToolCalls))
		for _, call := range groups[0].ToolCalls {
			decisions = append(decisions, codemodesession.ApprovalDecision{
				ToolCallID: call.ToolCallID,
				Approved:   false,
				Reason:     "blocked by policy",
			})
		}
		mustApplyApprovals(t, ctx, session, decisions...)

		out = session.Submit(ctx, `function toolCallStatusReport(refs: any[]): string {
  const views = refs.map((task: any) => $tool_call(task) as any);
  const lines = ["tool call status (" + views.length + "):"];
  for (let i = 0; i < views.length; i++) {
    const view = views[i];
    if (i > 0) lines.push("");
    lines.push((i + 1) + ". \"" + view.toolCallId + "\"");
    lines.push("   tool: " + view.toolName);
    lines.push("   status: " + view.status);
    if ("params" in view) lines.push("   params: " + inspect(view.params));
    if (view.status === "rejected") lines.push("   reason: " + view.reason);
    if (view.status === "failed") lines.push("   error: " + view.error);
    if (view.status === "success") lines.push("   result: " + inspect(view.result));
  }
  lines.push("");
  lines.push("Use $tool_call(\"<tool-call-id>\") to inspect a tool call again.");
  return lines.join("\n");
}
console.log(toolCallStatusReport((globalThis as any).tasks));
	"done"`)
		checkGolden(t, goldenPath("submit_multi_tool_rejection.txt"), normalizeToolCallIDs(out))
	})
}

func normalizeToolCallIDs(out string) string {
	return normalizeToolCallRefs(toolCallIDPattern.ReplaceAllString(out, `"toolCallId": "<tool-call-id>"`))
}

func normalizeToolCallRefs(out string) string {
	out = toolCallRefPattern.ReplaceAllStringFunc(out, func(string) string {
		return `$tool_call("<tool-call-id>")`
	})
	return toolCallLinePattern.ReplaceAllString(out, `$1"<tool-call-id>"`)
}

func prepareToolsetWithApprovals(t testing.TB, dir string, toolNames ...string) toolset.PreparedToolset {
	t.Helper()
	approvals := make(map[string]bool, len(toolNames))
	for _, toolName := range toolNames {
		approvals[toolName] = true
	}
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: approvals,
	})
}

func goldenPath(name string) string {
	return filepath.Join(testfileDir(), "..", "testutil", "testdata", "goldens", "codemodesession", name)
}

func checkGolden(t *testing.T, path string, got string) {
	t.Helper()
	if os.Getenv("GENERATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with GENERATE_GOLDENS=1 to create): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden mismatch (-want +got):\n%s", diff)
	}
}

func testfileDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
