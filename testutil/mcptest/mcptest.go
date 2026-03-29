package mcptest

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Harness wraps an in-process MCP client connected to a provided server.
type Harness struct {
	t      testing.TB
	Client *client.Client
	Server *server.MCPServer
}

// NewHarness creates, starts, and initializes an in-process MCP client for tests.
func NewHarness(t testing.TB, srv *server.MCPServer) *Harness {
	t.Helper()

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("create in-process client: %v", err)
	}

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

	if _, err := c.Initialize(context.Background(), initRequest); err != nil {
		t.Fatalf("initialize client: %v", err)
	}

	t.Cleanup(func() {
		_ = c.Close()
	})

	return &Harness{t: t, Client: c, Server: srv}
}

func (h *Harness) ListTools() *mcp.ListToolsResult {
	h.t.Helper()

	result, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		h.t.Fatalf("list tools: %v", err)
	}

	return result
}

func (h *Harness) ToolNames() []string {
	h.t.Helper()

	tools := h.ListTools()
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func (h *Harness) CallTool(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()

	result, err := h.Client.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		h.t.Fatalf("call tool %q: %v", name, err)
	}

	return result
}
