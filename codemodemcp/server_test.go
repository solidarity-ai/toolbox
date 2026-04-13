package codemodemcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodemcp"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestMCPServerListsSuperTool(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())
	tools := h.ListTools()
	names := h.ToolNames()

	assertSliceContains(t, names, codemodemcp.ToolSuperTool)
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "super_tool submits a code cell to a REPL")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "// REPL input")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "// REPL output")
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "$pkgMetadata")
}

func TestMCPServerCallsSuperTool(t *testing.T) {
	h := mcptest.NewHarness(t, codemodemcp.New())

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		"typescript_cell_source": "const value: number = 1\nvalue + 1",
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}

	text := resultText(t, result)
	assertTextContains(t, text, "cell 1")
	assertTextContains(t, text, "=> 2")
	next := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		"typescript_cell_source": "const value: number = 1",
	})
	if next.IsError {
		t.Fatalf("expected non-error result")
	}

	action := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		"typescript_cell_source": "value + 2",
	})
	if action.IsError {
		t.Fatalf("expected non-error result")
	}

	actionText := resultText(t, action)
	assertTextContains(t, actionText, "cell 2")
	assertTextContains(t, actionText, "=> 3")
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
	managed, err := codemodemcp.OpenManagedNamed(context.Background(), "example", t.TempDir())
	if err != nil {
		t.Fatalf("OpenManagedNamed(): %v", err)
	}
	defer managed.Close()

	h := mcptest.NewHarness(t, managed.Server())
	managed.SetPreparedTools(tooltest.PrepareToolset(t, tooltest.DistPackageDecl("calc"), toolset.Config{}))

	tools := h.ListTools()
	assertToolDescriptionContains(t, tools.Tools, codemodemcp.ToolSuperTool, "calc")

	result := h.CallTool(codemodemcp.ToolSuperTool, map[string]any{
		"typescript_cell_source": "calc.calc.add(2, 3)",
	})
	if result.IsError {
		t.Fatalf("expected non-error result")
	}
	text := resultText(t, result)
	assertTextContains(t, text, "=> 5")
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

func assertSliceContains(t testing.TB, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
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
