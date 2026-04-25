package codemodemcp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestMCPServerListsSuperTool(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())
	tools := h.ListTools()
	names := h.ToolNames()

	assertSliceContains(t, names, codemodemcp.ToolSuperTool)
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "super_tool submits a code cell to a notebook like environment")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "// Notebook Input")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "// Notebook Output")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "$pkgMetadata")
	assertToolPropertyDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, codemodesession.TimeoutSecsParam, "Maximum seconds to allow this cell to run")
}

func TestMCPServerListsAwaitSuperToolApprovalsOnlyWhenApprovalsArePossible(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())
	assertSliceNotContains(t, h.ToolNames(), codemodemcp.ToolAwaitSuperToolApprovals)

	dir := writeApprovalPackage(t)
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})
	withApprovals := mcptest.NewHarness(t, codemodemcp.New(codemodesession.SessionConfig{PreparedTools: prepared}))
	assertSliceContains(t, withApprovals.ToolNames(), codemodemcp.ToolAwaitSuperToolApprovals)
}

func TestUnlockedMCPToolSchemasRequireTBSession(t *testing.T) {
	dir := writeApprovalPackage(t)
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	h := mcptest.NewHarness(t, codemodemcp.New(codemodesession.SessionConfig{PreparedTools: prepared}))
	tools := h.ListTools()

	assertToolPropertyPresent(t, tools.Tools, codemodemcp.ToolSuperTool, codemodesession.TBSessionParam)
	assertToolPropertyPresent(t, tools.Tools, codemodemcp.ToolAwaitSuperToolApprovals, codemodesession.TBSessionParam)
	assertToolPropertyRequired(t, tools.Tools, codemodemcp.ToolSuperTool, codemodesession.TBSessionParam)
	assertToolPropertyRequired(t, tools.Tools, codemodemcp.ToolAwaitSuperToolApprovals, codemodesession.TBSessionParam)
}

func TestLockedMCPToolSchemasHideTBSession(t *testing.T) {
	tempDir := t.TempDir()
	dir := writeApprovalPackage(t)
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir), codemodesession.SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("OpenManagedBoundNamed(): %v", err)
	}
	defer managed.Close()

	h := mcptest.NewHarness(t, managed.Server())
	tools := h.ListTools()

	assertToolPropertyAbsent(t, tools.Tools, codemodemcp.ToolSuperTool, codemodesession.TBSessionParam)
	assertToolPropertyAbsent(t, tools.Tools, codemodemcp.ToolAwaitSuperToolApprovals, codemodesession.TBSessionParam)
	assertSliceNotContains(t, h.ToolNames(), codemodemcp.ToolNewSession)
}

func TestMCPServerCallsSuperTool(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())
	tbSession := mustNewTBSession(t, h)

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TBSessionParam: tbSession,
		"typescript_cell_source":       "const value: number = 1\nvalue + 1",
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	text := resultText(t, result)
	assertTextContains(t, text, "cell 1")
	assertTextContains(t, text, "=> 2")
	next := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TBSessionParam: tbSession,
		"typescript_cell_source":       "const value: number = 1",
	})
	if next.IsError {
		t.Fatalf("expected non-error result")
	}

	action := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TBSessionParam: tbSession,
		"typescript_cell_source":       "value + 2",
	})
	if action.IsError {
		t.Fatalf("expected non-error result")
	}

	actionText := resultText(t, action)
	assertTextContains(t, actionText, "cell 2")
	assertTextContains(t, actionText, "=> 3")
}

func TestMCPServerSuperToolUsesDefaultTimeout(t *testing.T) {
	originalTimeout := codemodesession.DefaultSubmitTimeout
	codemodesession.DefaultSubmitTimeout = 250 * time.Millisecond
	t.Cleanup(func() {
		codemodesession.DefaultSubmitTimeout = originalTimeout
	})

	h := mcptest.NewHarness(t, codemodemcp.New())
	tbSession := mustNewTBSession(t, h)

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TBSessionParam:            tbSession,
		codemodesession.TypeScriptCellSourceParam: `await new Promise(() => {})`,
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	text := resultText(t, result)
	assertTextContains(t, text, "cell (failed to commit)")
	assertTextContains(t, text, "context deadline exceeded")
}

func TestMCPServerDefaultName(t *testing.T) {
	initRes := initializeServer(t, codemodemcp.New())
	if initRes.ServerInfo.Name != "toolbox" {
		t.Fatalf("server name = %q, want toolbox", initRes.ServerInfo.Name)
	}
}

func TestMCPServerCustomName(t *testing.T) {
	initRes := initializeServer(t, codemodemcp.NewNamed("example"))
	if initRes.ServerInfo.Name != "example" {
		t.Fatalf("server name = %q, want example", initRes.ServerInfo.Name)
	}
}

func TestManagedMCPServerUpdatesSuperToolAtRuntime(t *testing.T) {
	tempDir := t.TempDir()
	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir))
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	h := mcptest.NewHarness(t, managed.Server())
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), toolset.Config{}))

	tools := h.ListTools()
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "calc")

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		"typescript_cell_source": "await calc.calc.add(2, 3)",
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	text := resultText(t, result)
	assertTextContains(t, text, "=> 5")
}

func TestManagedMCPServerUpdatesAwaitToolAtRuntime(t *testing.T) {
	tempDir := t.TempDir()
	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir))
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	h := mcptest.NewHarness(t, managed.Server())
	assertSliceNotContains(t, h.ToolNames(), codemodemcp.ToolAwaitSuperToolApprovals)

	dir := writeApprovalPackage(t)
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	}))
	assertSliceContains(t, h.ToolNames(), codemodemcp.ToolAwaitSuperToolApprovals)

	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), toolset.Config{}))
	assertSliceNotContains(t, h.ToolNames(), codemodemcp.ToolAwaitSuperToolApprovals)
}

func TestManagedMCPServerAwaitSuperToolApprovalsReturnsNoOutstandingWhenIdle(t *testing.T) {
	tempDir := t.TempDir()
	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir))
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	dir := writeApprovalPackage(t)
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	}))

	h := mcptest.NewHarness(t, managed.Server())
	result := h.CallTool(codemodemcp.ToolAwaitSuperToolApprovals, nil)
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	assertTextContains(t, resultText(t, result), "(no outstanding approvals).")
}

func TestManagedMCPServerAwaitSuperToolApprovalsReturnsResolutionAndRemaining(t *testing.T) {
	tempDir := t.TempDir()
	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir))
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	dir := writeApprovalPackage(t)
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	}))

	h := mcptest.NewHarness(t, managed.Server())
	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TypeScriptCellSourceParam: `var tasks = [issues.get("I-1"), issues.get("I-2")];
tasks.map((task) => $tool_call(task).status)`,
	})
	if result.IsError {
		t.Fatalf("super_tool expected non-error result")
	}

	approvals, err := managed.PendingApprovals(context.Background())
	if err != nil {
		t.Fatalf("PendingApprovals(): %v", err)
	}
	if len(approvals) != 2 {
		t.Fatalf("PendingApprovals() = %#v, want two calls", approvals)
	}

	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	waitCh := make(chan callResult, 1)
	go func() {
		res, err := h.Client.CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: codemodemcp.ToolAwaitSuperToolApprovals},
		})
		waitCh <- callResult{result: res, err: err}
	}()

	select {
	case got := <-waitCh:
		t.Fatalf("await returned early: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}

	if err := managed.ApplyApprovals(context.Background(), []codemodesession.ApprovalDecision{{
		ToolCallID: approvals[0].ToolCallID,
		Approved:   true,
	}}); err != nil {
		t.Fatalf("ApplyApprovals(): %v", err)
	}

	select {
	case got := <-waitCh:
		if got.err != nil {
			t.Fatalf("await call error: %v", got.err)
		}
		text := resultText(t, got.result)
		assertTextContains(t, text, "approved")
		assertTextContains(t, text, approvals[0].ToolCallID)
		assertTextContains(t, text, "1 still waiting, await again when ready.")
	case <-time.After(2 * time.Second):
		t.Fatal("await did not return after approval")
	}
}

func TestUnlockedMCPAwaitSuperToolApprovalsOnlyObservesRequestedSession(t *testing.T) {
	dir := writeApprovalPackage(t)
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})

	h := mcptest.NewHarness(t, codemodemcp.New(codemodesession.SessionConfig{PreparedTools: prepared}))
	tbSessionA := mustNewTBSession(t, h)
	tbSessionB := mustNewTBSession(t, h)

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TBSessionParam:            tbSessionB,
		codemodesession.TypeScriptCellSourceParam: `issues.get("I-2")`,
	})
	if result.IsError {
		t.Fatalf("super_tool expected non-error result")
	}

	await := h.CallTool(codemodemcp.ToolAwaitSuperToolApprovals, map[string]any{
		codemodesession.TBSessionParam: tbSessionA,
	})
	if await.IsError {
		t.Fatalf("await_super_tool_approvals expected non-error result")
	}
	assertTextContains(t, resultText(t, await), "(no outstanding approvals).")
}

func TestManagedMCPServerAwaitSuperToolApprovalsCancelledByLaterSuperTool(t *testing.T) {
	tempDir := t.TempDir()
	managed, err := codemodemcp.OpenManagedBoundNamed(context.Background(), "example", tempDir, mustCreateTBSession(t, tempDir))
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	dir := writeApprovalPackage(t)
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	}))

	h := mcptest.NewHarness(t, managed.Server())
	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		codemodesession.TypeScriptCellSourceParam: `issues.get("I-1")`,
	})
	if result.IsError {
		t.Fatalf("super_tool expected non-error result")
	}

	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	waitCh := make(chan callResult, 1)
	go func() {
		res, err := h.Client.CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: codemodemcp.ToolAwaitSuperToolApprovals},
		})
		waitCh <- callResult{result: res, err: err}
	}()

	select {
	case got := <-waitCh:
		t.Fatalf("await returned early: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = h.Client.CallTool(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: codemodemcp.ToolSuperTool,
				Arguments: map[string]any{
					codemodesession.TypeScriptCellSourceParam: `"next"`,
				},
			},
		})
	}()

	select {
	case got := <-waitCh:
		if got.err != nil {
			t.Fatalf("await call error: %v", got.err)
		}
		text := resultText(t, got.result)
		assertTextContains(t, text, "cancelled")
		assertTextContains(t, text, "1 still waiting, await again when ready.")
	case <-time.After(2 * time.Second):
		t.Fatal("await did not return after submit cancellation")
	}
	wg.Wait()
}

func resultText(t testing.TB, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("expected text content, got %#v", result.Content[0])
	}
	return text.Text
}

func mustNewTBSession(t testing.TB, h *mcptest.Harness) string {
	t.Helper()
	result := h.CallTool(codemodemcp.ToolNewSession, nil)
	if result.IsError {
		t.Fatalf("new_super_tool_session expected non-error result")
	}
	return strings.TrimSpace(resultText(t, result))
}

func mustCreateTBSession(t testing.TB, currentDir string) string {
	t.Helper()
	session, err := codemodesession.CreateFresh(context.Background(), currentDir)
	if err != nil {
		t.Fatalf("CreateFresh(): %v", err)
	}
	defer session.Close()
	return session.TBSession()
}

func assertSliceContains(t testing.TB, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
}

func assertSliceNotContains(t testing.TB, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			t.Fatalf("did not expect %q in %v", want, values)
		}
	}
}

func writeApprovalPackage(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"tool/package.json": `{"name":"issues","version":"0.0.1"}`,
		"tool/toolbox.devpkg.json": `{
  "module": "example.com/issues",
  "name": "issues",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/get.ts" }
  ]
}`,
		"tool/tools/get.ts": `export default async function tool(id: string): Promise<{ id: string }> {
  return { id };
}
`,
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	return filepath.Join(dir, "tool")
}

func assertTextContains(t testing.TB, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q in %q", want, got)
	}
}

func assertToolDescriptionContains(t testing.TB, tools []mcp.Tool, name, want string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if strings.Contains(tool.Description, want) {
			return
		}
		t.Fatalf("expected %q in description for %s, got %q", want, name, tool.Description)
	}
	t.Fatalf("tool %q not found", name)
}

func assertToolPropertyDescriptionContains(t testing.TB, tools []mcp.Tool, toolName, propertyName, want string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != toolName {
			continue
		}
		prop, ok := tool.InputSchema.Properties[propertyName].(map[string]any)
		if !ok {
			t.Fatalf("property %q missing from %s schema: %#v", propertyName, toolName, tool.InputSchema.Properties)
		}
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, want) {
			t.Fatalf("expected %q in description for %s.%s, got %q", want, toolName, propertyName, desc)
		}
		return
	}
	t.Fatalf("tool %q not found", toolName)
}

func assertToolPropertyPresent(t testing.TB, tools []mcp.Tool, toolName, propertyName string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != toolName {
			continue
		}
		if _, ok := tool.InputSchema.Properties[propertyName]; !ok {
			t.Fatalf("property %q missing from %s schema: %#v", propertyName, toolName, tool.InputSchema.Properties)
		}
		return
	}
	t.Fatalf("tool %q not found", toolName)
}

func assertToolPropertyAbsent(t testing.TB, tools []mcp.Tool, toolName, propertyName string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != toolName {
			continue
		}
		if _, ok := tool.InputSchema.Properties[propertyName]; ok {
			t.Fatalf("property %q unexpectedly present in %s schema: %#v", propertyName, toolName, tool.InputSchema.Properties)
		}
		return
	}
	t.Fatalf("tool %q not found", toolName)
}

func assertToolPropertyRequired(t testing.TB, tools []mcp.Tool, toolName, propertyName string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != toolName {
			continue
		}
		for _, required := range tool.InputSchema.Required {
			if required == propertyName {
				return
			}
		}
		t.Fatalf("property %q not required in %s schema: %#v", propertyName, toolName, tool.InputSchema.Required)
	}
	t.Fatalf("tool %q not found", toolName)
}

func initializeServer(t testing.TB, srv *server.MCPServer) *mcp.InitializeResult {
	t.Helper()

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}
	defer c.Close()

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("start client: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "toolbox-mcp-test-client",
		Version: "0.1.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	initRes, err := c.Initialize(context.Background(), initRequest)
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	return initRes
}
