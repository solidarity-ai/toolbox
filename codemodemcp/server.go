package codemodemcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/service"
)

const (
	ToolDiscoveryExecute = service.ToolDiscoveryExecute
	ToolActionExecute    = service.ToolActionExecute
)

// New creates an MCP server with the initial Toolbox MCP surface.
//
// The server currently delegates to stub service-layer implementations so the
// outside-in caller contracts can be validated before real codemode wiring is added.
func New() *server.MCPServer {
	mcpServer := server.NewMCPServer(
		"toolbox-codemode-mcp-server",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	mcpServer.AddTool(newDiscoveryTool(), handleDiscoveryExecute)
	mcpServer.AddTool(newActionTool(), handleActionExecute)

	return mcpServer
}

func newDiscoveryTool() mcp.Tool {
	return mcp.NewTool(
		ToolDiscoveryExecute,
		mcp.WithDescription("Run discovery-oriented code against the current tool environment and return a toolboxID for later action execution."),
		mcp.WithString("code", mcp.Required(), mcp.Description("Discovery-oriented code to execute.")),
	)
}

func newActionTool() mcp.Tool {
	return mcp.NewTool(
		ToolActionExecute,
		mcp.WithDescription("Run action-oriented code against the immutable toolbox snapshot identified by toolboxID."),
		mcp.WithString("toolboxID", mcp.Required(), mcp.Description("Opaque toolbox snapshot handle returned by tool_discovery_execute.")),
		mcp.WithString("code", mcp.Required(), mcp.Description("Action-oriented code to execute.")),
	)
}

func handleDiscoveryExecute(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString("code")
	if err != nil {
		return nil, err
	}

	return service.ExecuteToolDiscovery(ctx, service.ToolDiscoveryExecuteRequest{
		Code: code,
	})
}

func handleActionExecute(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	toolboxID, err := request.RequireString("toolboxID")
	if err != nil {
		return nil, err
	}

	code, err := request.RequireString("code")
	if err != nil {
		return nil, err
	}

	return service.ExecuteToolAction(ctx, service.ToolActionExecuteRequest{
		ToolboxID: toolboxID,
		Code:      code,
	})
}
