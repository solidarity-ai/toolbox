package codemodesession_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestOpenMemorySubmitReturnsSharedReplOutput(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedTool("gmail.listThreads", "gmail"),
			newPreparedTool("gmail.sendDraft", "gmail"),
			newPreparedTool("hackerNews.frontPage", "hacker_news"),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `Object.entries($pkgMetadata).map(([packageName, meta]) => [packageName, meta.toolCount])`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "gmail")
	assertContains(t, out, "hacker_news")
	assertContains(t, out, "--")
}

func TestPackagesAPIContainsPackageDeclarationSource(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedTool("gmail.listThreads", "gmail"),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata["gmail"]?.api.includes("declare namespace gmail")`)
	assertContains(t, out, "=> true")
}

func TestPackageMetadataExposesUseWhenHint(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: toolset.NewPreparedToolset([]assembler.LoadedTool{
			newPreparedToolWithUseWhenHint("hn.frontPage", "hacker_news", "Use when you need Hacker News posts and comments."),
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata["hacker_news"]?.useWhenHint?.includes("Hacker News posts and comments.")`)
	assertContains(t, out, "=> true")
}

func TestSubmitPackageMetadataWorksWithInjectedAccountParamAfterOptionalInput(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/mail", "mail", map[string]string{
		"tools/list.ts": `export default async function tool(input?: {
  query?: string;
}): Promise<{ ok: boolean }> {
  return { ok: true };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("example.com/mail"): {
					CredentialAccounts: map[string][]string{
						"workspace": {"a@example.com", "b@example.com"},
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `$pkgMetadata`)
	assertContains(t, out, "mail")
	assertNotContains(t, out, "failure: typecheck failed")
	assertNotContains(t, out, "A required parameter cannot follow an optional parameter")

	countOut := session.Submit(ctx, `$pkgMetadata["mail"]?.toolCount`)
	assertContains(t, countOut, "=> 1")
	assertNotContains(t, countOut, "failure: typecheck failed")
	assertNotContains(t, countOut, "A required parameter cannot follow an optional parameter")
}

func TestInstructionsIncludeUnlockNoteForLockedPackages(t *testing.T) {
	t.Setenv(daemon.BindAddressEnv, "localhost:7113")

	prepared, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{
		newPreparedTool("locked.listThreads", "gmail"),
	}, toolset.Config{
		CredentialPolicySource: lockedPolicySource{},
	})
	if err != nil {
		t.Fatalf("PrepareTools() error: %v", err)
	}

	session, err := codemodesession.OpenMemory(context.Background(), t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepared,
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	instructions := session.Instructions()
	assertContains(t, instructions, "Some tools are unavailable because the toolbox secret store is locked: gmail.")
	assertContains(t, instructions, "Unlock Toolbox at http://localhost:7113/ and reload to restore them.")
}

func TestSubmitCanCallPreparedToolPackages(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `await calc.calc.add(2, 3)`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "=> 5")
}

func TestSubmitDoesNotExposeRuntimeHashToken(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const p = calc.calc.add(2, 3); (globalThis as any).__toolboxCurrentRuntimeHash === undefined && typeof p.task.toolCallId === "string" && p.task.toolCallId.length > 0 && (await p) === 5`)
	assertContains(t, out, "=> true")
}

func TestSubmitToolCallPromiseHasTaskAndSupportsInspection(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example" };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const p = issues.get("I-1");
const taskId = p.task.toolCallId;
const value = await p;
const view = $tool_call(p);
console.log(JSON.stringify({
  hasTaskId: typeof taskId === "string" && taskId.length > 0,
  status: view.status,
  title: value.title,
  resultTitle: view.status === "success" ? view.result.title : null,
}))`)
	assertContains(t, out, `"hasTaskId":true`)
	assertContains(t, out, `"status":"success"`)
	assertContains(t, out, `"title":"Example"`)
	assertContains(t, out, `"resultTitle":"Example"`)
}

func TestSubmitToolCallTaskNeedsApprovalAndSupportsInspection(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example" };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const task = issues.get("I-1");
const view = $tool_call(task);
console.log(JSON.stringify({
  hasTaskId: typeof task.toolCallId === "string" && task.toolCallId.length > 0,
  status: view.status,
  toolName: view.toolName,
  hasTaskProperty: Object.prototype.hasOwnProperty.call(task as any, "task"),
}))`)
	assertContains(t, out, `"hasTaskId":true`)
	assertContains(t, out, `"status":"needsApproval"`)
	assertContains(t, out, `"toolName":"get"`)
	assertContains(t, out, `"hasTaskProperty":false`)
}

func TestOpenSQLiteGroupsPendingApprovalCallsByCellAndApprovesThemTogether(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example " + id };
}
`,
	})
	sqlitePath := filepath.Join(t.TempDir(), "session.sqlite")

	session, err := codemodesession.OpenSQLite(ctx, sqlitePath, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}

	out := session.Submit(ctx, `var tasks = [issues.get("I-1"), issues.get("I-2")];
tasks.map((task) => $tool_call(task).status)`)
	assertContains(t, out, `"needsApproval"`)

	groups := mustPendingApprovalGroups(t, ctx, session)
	if len(groups) != 1 {
		t.Fatalf("pending approval groups len = %d, want 1", len(groups))
	}
	if len(groups[0].ToolCalls) != 2 {
		t.Fatalf("pending approval groups[0].ToolCalls len = %d, want 2", len(groups[0].ToolCalls))
	}
	if groups[0].CellID == "" {
		t.Fatal("pending approval groups[0].CellID = empty, want committed cell id")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close(first) error: %v", err)
	}

	reopened, err := codemodesession.OpenSQLite(ctx, sqlitePath, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(reopen) error: %v", err)
	}
	defer reopened.Close()

	reopenedGroups := mustPendingApprovalGroups(t, ctx, reopened)
	if len(reopenedGroups) != 1 {
		t.Fatalf("pending approval groups(reopen) len = %d, want 1", len(reopenedGroups))
	}
	if reopenedGroups[0].CellID != groups[0].CellID {
		t.Fatalf("pending approval groups(reopen)[0].CellID = %q, want %q", reopenedGroups[0].CellID, groups[0].CellID)
	}

	decisions := make([]codemodesession.ApprovalDecision, 0, len(reopenedGroups[0].ToolCalls))
	for _, call := range reopenedGroups[0].ToolCalls {
		decisions = append(decisions, codemodesession.ApprovalDecision{
			ToolCallID: call.ToolCallID,
			Approved:   true,
		})
	}
	mustApplyApprovals(t, ctx, reopened, decisions...)

	afterApprove := mustPendingApprovalGroups(t, ctx, reopened)
	if len(afterApprove) != 0 {
		t.Fatalf("pending approval groups(after approve) len = %d, want 0", len(afterApprove))
	}

	inspect := reopened.Submit(ctx, `console.log(JSON.stringify((globalThis as any).tasks.map((task: any) => $tool_call(task))));
"done"`)
	assertContains(t, inspect, `"status":"success"`)
	assertContains(t, inspect, `"id":"I-1"`)
	assertContains(t, inspect, `"id":"I-2"`)
}

func TestOpenSQLiteAllowsMixedApprovalActionsWithinOneCellGroup(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(input: {
  id: string;
  meta?: {
    team?: {
      owner?: {
        name: string;
      };
    };
  };
}): Promise<{ id: string; owner?: string }> {
  return { id: input.id, owner: input.meta?.team?.owner?.name };
}
`,
	})

	session, err := codemodesession.OpenSQLite(ctx, filepath.Join(t.TempDir(), "session.sqlite"), t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `var tasks = [
  issues.get({ id: "I-1", meta: { team: { owner: { name: "alpha" } } } }),
  issues.get({ id: "I-2", meta: { team: { owner: { name: "beta" } } } }),
  issues.get({ id: "I-3", meta: { team: { owner: { name: "gamma" } } } }),
];
tasks.map((task) => $tool_call(task).status)`)
	assertContains(t, out, `"needsApproval"`)

	groups := mustPendingApprovalGroups(t, ctx, session)
	if len(groups) != 1 {
		t.Fatalf("pending approval groups len = %d, want 1", len(groups))
	}
	if len(groups[0].ToolCalls) != 3 {
		t.Fatalf("pending approval groups[0].ToolCalls len = %d, want 3", len(groups[0].ToolCalls))
	}
	assertContains(t, groups[0].ToolCalls[0].ParamsInspect, `owner: {`)
	assertContains(t, groups[0].ToolCalls[0].ParamsInspect, `"I-1"`)
	assertContains(t, groups[0].ToolCalls[1].ParamsInspect, `owner: {`)
	assertContains(t, groups[0].ToolCalls[1].ParamsInspect, `"I-2"`)
	assertContains(t, groups[0].ToolCalls[2].ParamsInspect, `owner: {`)
	assertContains(t, groups[0].ToolCalls[2].ParamsInspect, `"I-3"`)

	mustApplyApprovals(t, ctx, session, codemodesession.ApprovalDecision{
		ToolCallID: groups[0].ToolCalls[0].ToolCallID,
		Approved:   true,
	})

	afterSingleApprove := mustPendingApprovalGroups(t, ctx, session)
	if len(afterSingleApprove) != 1 {
		t.Fatalf("pending approval groups(after single approve) len = %d, want 1", len(afterSingleApprove))
	}
	if afterSingleApprove[0].CellID != groups[0].CellID {
		t.Fatalf("pending approval groups(after single approve)[0].CellID = %q, want %q", afterSingleApprove[0].CellID, groups[0].CellID)
	}
	if len(afterSingleApprove[0].ToolCalls) != 2 {
		t.Fatalf("pending approval groups(after single approve)[0].ToolCalls len = %d, want 2", len(afterSingleApprove[0].ToolCalls))
	}

	mustApplyApprovals(t, ctx, session, codemodesession.ApprovalDecision{
		ToolCallID: afterSingleApprove[0].ToolCalls[0].ToolCallID,
		Approved:   false,
		Reason:     "manual reject",
	})

	afterSingleReject := mustPendingApprovalGroups(t, ctx, session)
	if len(afterSingleReject) != 1 {
		t.Fatalf("pending approval groups(after single reject) len = %d, want 1", len(afterSingleReject))
	}
	if len(afterSingleReject[0].ToolCalls) != 1 {
		t.Fatalf("pending approval groups(after single reject)[0].ToolCalls len = %d, want 1", len(afterSingleReject[0].ToolCalls))
	}

	mustApplyApprovals(t, ctx, session, codemodesession.ApprovalDecision{
		ToolCallID: afterSingleReject[0].ToolCalls[0].ToolCallID,
		Approved:   true,
	})

	afterGroupApprove := mustPendingApprovalGroups(t, ctx, session)
	if len(afterGroupApprove) != 0 {
		t.Fatalf("pending approval groups(after group approve) len = %d, want 0", len(afterGroupApprove))
	}

	inspect := session.Submit(ctx, `console.log(JSON.stringify((globalThis as any).tasks.map((task: any) => $tool_call(task))));
"done"`)
	assertContains(t, inspect, `"status":"success"`)
	assertContains(t, inspect, `"status":"failed"`)
	assertContains(t, inspect, `"error":"manual reject"`)
	assertContains(t, inspect, `"id":"I-1"`)
	assertContains(t, inspect, `"id":"I-3"`)
	assertContains(t, inspect, `"owner":"alpha"`)
	assertContains(t, inspect, `"owner":"gamma"`)
}

func TestOpenSQLiteRejectsEntireApprovalGroupWithMessage(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string }> {
  return { id };
}
`,
	})

	session, err := codemodesession.OpenSQLite(ctx, filepath.Join(t.TempDir(), "session.sqlite"), t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `var tasks = [issues.get("I-1"), issues.get("I-2")];
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

	afterReject := mustPendingApprovalGroups(t, ctx, session)
	if len(afterReject) != 0 {
		t.Fatalf("pending approval groups(after reject) len = %d, want 0", len(afterReject))
	}

	inspect := session.Submit(ctx, `console.log(JSON.stringify((globalThis as any).tasks.map((task: any) => $tool_call(task))));
"done"`)
	assertContains(t, inspect, `"status":"failed"`)
	assertContains(t, inspect, `"error":"blocked by policy"`)
}

func TestAwaitNextApprovalReturnsNoOutstandingWhenIdle(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	result, err := session.AwaitNextApproval(ctx)
	if err != nil {
		t.Fatalf("AwaitNextApproval() error: %v", err)
	}
	if result.Status != codemodesession.ApprovalAwaitStatusNoOutstanding {
		t.Fatalf("AwaitNextApproval().Status = %q, want %q", result.Status, codemodesession.ApprovalAwaitStatusNoOutstanding)
	}
	if got := result.Text(); got != "(no outstanding approvals)." {
		t.Fatalf("AwaitNextApproval().Text() = %q, want %q", got, "(no outstanding approvals).")
	}
}

func TestAwaitNextApprovalReturnsResolvedCallAndRemaining(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string }> {
  return { id };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `var tasks = [issues.get("I-1"), issues.get("I-2")];
tasks.map((task) => $tool_call(task).status)`)
	assertContains(t, out, `"needsApproval"`)

	groups := mustPendingApprovalGroups(t, ctx, session)
	if len(groups) != 1 || len(groups[0].ToolCalls) != 2 {
		t.Fatalf("pending approval groups = %#v, want one group with two calls", groups)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resultCh := make(chan codemodesession.ApprovalAwaitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := session.AwaitNextApproval(waitCtx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("AwaitNextApproval() returned early: %#v", result)
	case err := <-errCh:
		t.Fatalf("AwaitNextApproval() returned early error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	mustApplyApprovals(t, ctx, session, codemodesession.ApprovalDecision{
		ToolCallID: groups[0].ToolCalls[0].ToolCallID,
		Approved:   true,
	})

	select {
	case err := <-errCh:
		t.Fatalf("AwaitNextApproval() error: %v", err)
	case result := <-resultCh:
		if result.Status != codemodesession.ApprovalAwaitStatusApproved {
			t.Fatalf("AwaitNextApproval().Status = %q, want %q", result.Status, codemodesession.ApprovalAwaitStatusApproved)
		}
		if result.ToolCall.ToolCallID != groups[0].ToolCalls[0].ToolCallID {
			t.Fatalf("AwaitNextApproval().ToolCall.ToolCallID = %q, want %q", result.ToolCall.ToolCallID, groups[0].ToolCalls[0].ToolCallID)
		}
		if result.Remaining != 1 {
			t.Fatalf("AwaitNextApproval().Remaining = %d, want 1", result.Remaining)
		}
		assertContains(t, result.Text(), "approved")
		assertContains(t, result.Text(), "1 still waiting, await again when ready.")
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitNextApproval() did not return after approval")
	}
}

func TestAwaitNextApprovalCancelledByLaterSubmit(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string }> {
  return { id };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
			ToolApprovals: map[string]bool{
				"issues.get": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `issues.get("I-1")`)
	assertContains(t, out, `toolCallId`)

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resultCh := make(chan codemodesession.ApprovalAwaitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := session.AwaitNextApproval(waitCtx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	select {
	case <-resultCh:
		t.Fatal("AwaitNextApproval() returned early")
	case err := <-errCh:
		t.Fatalf("AwaitNextApproval() returned early error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	next := session.Submit(ctx, `"next"`)
	assertContains(t, next, "next")

	select {
	case err := <-errCh:
		t.Fatalf("AwaitNextApproval() error: %v", err)
	case result := <-resultCh:
		if result.Status != codemodesession.ApprovalAwaitStatusCancelled {
			t.Fatalf("AwaitNextApproval().Status = %q, want %q", result.Status, codemodesession.ApprovalAwaitStatusCancelled)
		}
		if result.Remaining != 1 {
			t.Fatalf("AwaitNextApproval().Remaining = %d, want 1", result.Remaining)
		}
		assertContains(t, result.Text(), "cancelled")
		assertContains(t, result.Text(), "1 still waiting, await again when ready.")
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitNextApproval() did not return after submit cancellation")
	}
}

func TestSubmitToolCallIDsIgnoreUserGlobalMutation(t *testing.T) {
	ctx := context.Background()
	dir := writeSessionPackage(t, t.TempDir(), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example" };
}
`,
	})

	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{}),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const first = issues.get("I-1");
await first;
(globalThis as any).__toolbox_tool_call_seq = 1;
const second = issues.get("I-2");
await second;
const secondView = $tool_call(second);
const params = secondView.params as any;
console.log(JSON.stringify({
  sameId: first.task.toolCallId === second.task.toolCallId,
  status: secondView.status,
  paramId: params?.id ?? null,
  resultId: secondView.status === "success" ? secondView.result.id : null,
}))`)
	assertContains(t, out, `"sameId":false`)
	assertContains(t, out, `"status":"success"`)
	assertContains(t, out, `"paramId":"I-2"`)
	assertContains(t, out, `"resultId":"I-2"`)
}

func TestSetPreparedTools_AddsBindingsBeforeFirstSubmit(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(prepareCalcToolset(t))
	out := session.Submit(ctx, `await calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestSetPreparedTools_RemovesBindingsAfterCommittedCells(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, session.Submit(ctx, `"alpha"`), "alpha")

	session.SetPreparedTools(toolset.PreparedToolset{})

	failed := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, failed, "failure:")
	assertContains(t, failed, "tool calc.add came from a previous runtime and is no longer callable")

	out := session.Submit(ctx, `$val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
}

func TestSetPreparedTools_WrappersDoNotExposeInternalMetadata(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")

	initial := session.Submit(ctx, `!Object.prototype.hasOwnProperty.call($val(1), "__replRuntimeHash") && !Object.prototype.hasOwnProperty.call($val(1), "__replStaleIndexedMessage")`)
	assertContains(t, initial, "=> true")

	session.SetPreparedTools(toolset.PreparedToolset{})

	stale := session.Submit(ctx, `!Object.prototype.hasOwnProperty.call($val(1), "__replStaleIndexedMessage")`)
	assertContains(t, stale, "=> true")
}

func TestSetPreparedTools_LastTransitionWinsAtSameHead(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(prepareCalcToolset(t))
	session.SetPreparedTools(toolset.PreparedToolset{})
	session.SetPreparedTools(prepareCalcToolset(t))

	out := session.Submit(ctx, `await calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestSetPreparedTools_SavedWrapperRejectedAfterRuntimeChangeEvenWhenToolStillExists(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")

	session.SetPreparedTools(prepareCalcAndEdgeCasesToolset(t))

	staleLast := session.Submit(ctx, `($last as any)(2, 3)`)
	assertContains(t, staleLast, "failure:")
	assertContains(t, staleLast, "tool calc.add came from a previous runtime and is no longer callable")

	stale := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, stale, "failure:")
	assertContains(t, stale, "tool calc.add came from a previous runtime and is no longer callable")

	fresh := session.Submit(ctx, `await calc.calc.add(2, 3)`)
	assertContains(t, fresh, "=> 5")
}

func TestSetPreparedTools_RebuildsDirtyRuntimeBeforeTransition(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir(), codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	assertContains(t, session.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, session.Submit(ctx, `"alpha"`), "alpha")

	failed := session.Submit(ctx, `globalThis.leaked = 1; throw new Error("boom")`)
	assertContains(t, failed, "failure:")

	session.SetPreparedTools(toolset.PreparedToolset{})

	removed := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, removed, "failure:")
	assertContains(t, removed, "tool calc.add came from a previous runtime and is no longer callable")

	out := session.Submit(ctx, `((globalThis as any).leaked === undefined) && $val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := session.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
}

func TestSubmitRendersTypeScriptDiagnostics(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `const value: number = "x"`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "failure: typecheck failed")
	assertContains(t, out, "diagnostics:")
	assertContains(t, out, "error:")
	assertNotContains(t, out, "submit error:")
}

func TestSubmitTranslatesWrappedObjectLiteralDiagnosticsBackToUserColumns(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `{ a: missingVar }`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "1:6 error:")
	assertContains(t, out, "Cannot find name 'missingVar'.")
	assertNotContains(t, out, "1:7 error:")
}

func TestSubmitPrintsConsoleLogs(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `console.log("ok", { a: 1 }); 1`)
	assertContains(t, out, "cell 1")
	assertContains(t, out, "--\nok [object Object]\n=> 1\n")
}

func TestSubmitPrintsConsoleLogsOnFailure(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	out := session.Submit(ctx, `console.log("bad", { a: 2 }); throw new Error("boom")`)
	assertContains(t, out, "cell (failed to commit)")
	assertContains(t, out, "failure:")
	assertContains(t, out, "--\nbad [object Object]\n")
	assertNotContains(t, out, "submit error:")
}

func TestOpenSQLiteResumesLatestSession(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	if first.Resumed() {
		t.Fatal("first session resumed = true, want false")
	}
	assertContains(t, first.Submit(ctx, "const value: number = 1"), "cell 1")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, "value + 2")
	assertContains(t, out, "=> 3")
}

func TestOpenSQLiteReopensToolCellsWithoutPreparedTools(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `await calc.calc.add(2, 3)`), `=> 5`)
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()
	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `const ok: number = 1; ok`)
	assertContains(t, out, "warn: TypeScript static context was reset because the TypeScript env changed.")
	assertContains(t, out, "=> 1")
}

func TestOpenSQLiteResumesWithCurrentPreparedToolsBeforeFirstSubmit(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-live-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `await calc.calc.add(2, 3)`)
	assertContains(t, out, "=> 5")
}

func TestOpenSQLiteResumesCommittedValuesAndAddsCurrentPreparedTools(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-live-tools-with-history.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `"alpha"`), "alpha")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	out := second.Submit(ctx, `$val(1) === "alpha" && (await calc.calc.add(2, 3)) === 5`)
	assertContains(t, out, "warn: TypeScript static context was reset because the TypeScript env changed.")
	assertContains(t, out, "=> true")
}

func TestOpenSQLiteReopensToolCallTaskHistory(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-tool-calls.toolbox-session")
	dir := writeSessionPackage(t, filepath.Join(tempDir, "issues"), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example" };
}
`,
	})
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepared,
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `issues.get("I-1")`), "cell 1")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	out := second.Submit(ctx, `const task = $val(1) as { toolCallId: string };
const view = $tool_call(task);
console.log(JSON.stringify({
  hasTaskId: typeof task.toolCallId === "string" && task.toolCallId.length > 0,
  status: view.status,
  toolName: view.toolName,
}))`)
	assertContains(t, out, `"hasTaskId":true`)
	assertContains(t, out, `"status":"needsApproval"`)
	assertContains(t, out, `"toolName":"get"`)
}

func TestOpenSQLiteResumedSessionUsesFreshToolCallIDs(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-tool-call-seq.toolbox-session")
	dir := writeSessionPackage(t, filepath.Join(tempDir, "issues"), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "Example " + id };
}
`,
	})
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepared,
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}

	out := first.Submit(ctx, `var firstTask = issues.get("I-1");
const firstView = $tool_call(firstTask);
console.log(JSON.stringify({
  toolCallId: firstTask.toolCallId,
  status: firstView.status,
  paramId: (firstView.params as any)?.id ?? null,
}))`)
	assertContains(t, out, `"status":"needsApproval"`)
	assertContains(t, out, `"paramId":"I-1"`)

	firstGroups := mustPendingApprovalGroups(t, ctx, first)
	if len(firstGroups) != 1 || len(firstGroups[0].ToolCalls) != 1 {
		t.Fatalf("pending approval groups(first) = %#v, want one group with one call", firstGroups)
	}
	firstToolCallID := firstGroups[0].ToolCalls[0].ToolCallID

	mustApplyApprovals(t, ctx, first, codemodesession.ApprovalDecision{
		ToolCallID: firstToolCallID,
		Approved:   true,
	})
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepared,
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	out = second.Submit(ctx, `var secondTask = issues.get("I-2");
const secondView = $tool_call(secondTask);
console.log(JSON.stringify({
  toolCallId: secondTask.toolCallId,
  status: secondView.status,
  paramId: (secondView.params as any)?.id ?? null,
  hasResult: Object.prototype.hasOwnProperty.call(secondView as any, "result"),
}))`)
	assertContains(t, out, `"status":"needsApproval"`)
	assertContains(t, out, `"paramId":"I-2"`)
	assertContains(t, out, `"hasResult":false`)

	secondGroups := mustPendingApprovalGroups(t, ctx, second)
	if len(secondGroups) != 1 || len(secondGroups[0].ToolCalls) != 1 {
		t.Fatalf("pending approval groups(second) = %#v, want one group with one call", secondGroups)
	}
	secondToolCallID := secondGroups[0].ToolCalls[0].ToolCallID
	if secondToolCallID == firstToolCallID {
		t.Fatalf("second tool call id = %q, want a fresh id distinct from %q", secondToolCallID, firstToolCallID)
	}
}

func TestOpenSQLiteRejectsApprovalWhenPreparedToolChangesAfterReview(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-approval-tool-drift.toolbox-session")
	firstDir := writeSessionPackage(t, filepath.Join(tempDir, "issues-v1"), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "v1-" + id };
}
`,
	})
	secondDir := writeSessionPackage(t, filepath.Join(tempDir, "issues-v2"), "example.com/issues", "issues", map[string]string{
		"tools/get.ts": `export default async function tool(id: string): Promise<{ id: string; title: string }> {
  return { id, title: "v2-" + id };
}
`,
	})

	firstPrepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(firstDir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})
	secondPrepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(secondDir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: firstPrepared,
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}

	out := first.Submit(ctx, `var reviewedTask = issues.get("I-1");
const reviewedView = $tool_call(reviewedTask);
console.log(JSON.stringify({
  toolCallId: reviewedTask.toolCallId,
  status: reviewedView.status,
  paramId: (reviewedView.params as any)?.id ?? null,
}))`)
	assertContains(t, out, `"status":"needsApproval"`)
	assertContains(t, out, `"paramId":"I-1"`)

	groups := mustPendingApprovalGroups(t, ctx, first)
	if len(groups) != 1 || len(groups[0].ToolCalls) != 1 {
		t.Fatalf("pending approval groups(first) = %#v, want one group with one call", groups)
	}
	toolCallID := groups[0].ToolCalls[0].ToolCallID

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: secondPrepared,
	})
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	mustApplyApprovals(t, ctx, second, codemodesession.ApprovalDecision{
		ToolCallID: toolCallID,
		Approved:   true,
	})

	inspect := second.Submit(ctx, `const view = $tool_call({ toolCallId: "`+toolCallID+`" });
console.log(JSON.stringify(view));
"done"`)
	assertContains(t, inspect, `"status":"failed"`)
	assertContains(t, inspect, `"toolName":"get"`)
	assertContains(t, inspect, `"toolCallId":"`+toolCallID+`"`)
	assertContains(t, inspect, `approved tool issues.get no longer matches the reviewed version`)
	assertNotContains(t, inspect, `"title":"v2-I-1"`)
}

func TestOpenSQLiteResumesWithCurrentPreparedToolsAfterCommittedCells(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "codemode-remove-tools.toolbox-session")

	first, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir, codemodesession.SessionConfig{
		PreparedTools: prepareCalcToolset(t),
	})
	if err != nil {
		t.Fatalf("OpenSQLite(first) error: %v", err)
	}
	assertContains(t, first.Submit(ctx, `calc.calc.add`), "cell 1")
	assertContains(t, first.Submit(ctx, `"alpha"`), "alpha")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}

	second, err := codemodesession.OpenSQLite(ctx, dbPath, tempDir)
	if err != nil {
		t.Fatalf("OpenSQLite(second) error: %v", err)
	}
	defer second.Close()

	if !second.Resumed() {
		t.Fatal("second session resumed = false, want true")
	}
	failed := second.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, failed, "failure:")
	assertContains(t, failed, "tool calc.add came from a previous runtime and is no longer callable")

	out := second.Submit(ctx, `$val(2) === "alpha" && ((globalThis as any).calc === undefined) && ($pkgMetadata["calc"] === undefined)`)
	assertContains(t, out, "=> true")

	gone := second.Submit(ctx, `($val(1) as any)(2, 3)`)
	assertContains(t, gone, "failure:")
	assertContains(t, gone, "tool calc.add came from a previous runtime and is no longer callable")
}

func TestInstructionsDescribeSessionUsage(t *testing.T) {
	ctx := context.Background()
	session, err := codemodesession.OpenMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenMemory() error: %v", err)
	}
	defer session.Close()

	session.SetPreparedTools(toolset.NewPreparedToolset([]assembler.LoadedTool{
		newPreparedTool("gmail.listThreads", "gmail"),
		newPreparedTool("gmail.sendDraft", "gmail"),
		newPreparedToolWithUseWhenHint("hackerNews.frontPage", "hacker_news", "Use when you need Hacker News posts and comments."),
	}))
	instructions := session.Instructions()
	assertContains(t, instructions, "super_tool submits a code cell to a notebook like environment")
	assertContains(t, instructions, "super_tool is much more efficient and effective than regular tool calling")
	assertContains(t, instructions, "$pkgMetadata")
	assertContains(t, instructions, "declare const $pkgMetadata: Record<string, {")
	assertContains(t, instructions, "toolCount: number;")
	assertContains(t, instructions, "present when the package needs extra guidance")
	assertContains(t, instructions, "useWhenHint?: string;")
	assertContains(t, instructions, "/* .d.ts for package, always use console.log to view */")
	assertContains(t, instructions, "meta.toolCount")
	assertContains(t, instructions, "meta.useWhenHint")
	assertContains(t, instructions, "// Notebook Input")
	assertContains(t, instructions, "// Notebook Output")
	assertContains(t, instructions, `["gmail", 2, ""]`)
	assertContains(t, instructions, `["hacker_news", 1, "Use when you need Hacker News posts and comments."]`)
	assertContains(t, instructions, "last value in an expression in prior cell")
	assertContains(t, instructions, "$val(<last-cell>)")
	assertContains(t, instructions, "$last : any")
	assertContains(t, instructions, "$val(index : number) : any")
	assertContains(t, instructions, "inspect(x : any) : string")
}

type pendingApprovalGroupForTest struct {
	CellID    string
	ToolCalls []codemodesession.PendingApproval
}

func pendingApprovalGroupsByCell(approvals []codemodesession.PendingApproval) []pendingApprovalGroupForTest {
	if len(approvals) == 0 {
		return nil
	}
	indexByCell := make(map[string]int)
	out := make([]pendingApprovalGroupForTest, 0, len(approvals))
	for _, approval := range approvals {
		idx, ok := indexByCell[approval.CellID]
		if !ok {
			idx = len(out)
			indexByCell[approval.CellID] = idx
			out = append(out, pendingApprovalGroupForTest{CellID: approval.CellID})
		}
		out[idx].ToolCalls = append(out[idx].ToolCalls, approval)
	}
	return out
}

func mustPendingApprovalGroups(t testing.TB, ctx context.Context, session *codemodesession.Session) []pendingApprovalGroupForTest {
	t.Helper()
	approvals, err := session.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals() error: %v", err)
	}
	return pendingApprovalGroupsByCell(approvals)
}

func mustApplyApprovals(t testing.TB, ctx context.Context, session *codemodesession.Session, decisions ...codemodesession.ApprovalDecision) {
	t.Helper()
	if err := session.ApplyApprovals(ctx, decisions); err != nil {
		t.Fatalf("ApplyApprovals() error: %v", err)
	}
}

func newPreparedTool(name, packageName string) assembler.LoadedTool {
	return newPreparedToolWithUseWhenHint(name, packageName, "")
}

func prepareCalcToolset(t testing.TB) toolset.PreparedToolset {
	t.Helper()
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
}

func prepareCalcAndEdgeCasesToolset(t testing.TB) toolset.PreparedToolset {
	t.Helper()
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc", "edge-cases"), toolset.Config{})
}

func newPreparedToolWithUseWhenHint(name, packageName, useWhenHint string) assembler.LoadedTool {
	return assembler.LoadedTool{
		Name: name,
		PackageMeta: &tooldef.Package{
			Name:        packageName,
			UseWhenHint: useWhenHint,
		},
	}
}

func writeSessionPackage(t testing.TB, dir, module, name string, files map[string]string) string {
	t.Helper()

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(files[path]), 0o644); err != nil {
			t.Fatalf("write %s: %v", fullPath, err)
		}
	}

	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, fmt.Sprintf(`{ "entry_ts": %q }`, path))
	}
	manifest := fmt.Sprintf("{\n  \"module\": %q,\n  \"name\": %q,\n  \"runtime\": \"typescript-sandbox\",\n  \"tools\": [\n    %s\n  ]\n}\n", module, name, strings.Join(entries, ",\n    "))
	if err := os.WriteFile(filepath.Join(dir, "toolbox.devpkg.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

type lockedPolicySource struct{}

func (lockedPolicySource) PackageCredentialPolicy(context.Context, tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	return toolset.PackageCredentialPolicy{}, secrets.ErrLocked
}

func assertContains(t testing.TB, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t testing.TB, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output unexpectedly contained %q\noutput:\n%s", want, got)
	}
}
