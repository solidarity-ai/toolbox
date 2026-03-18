package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ToolDiscoveryExecute = "tool_discovery_execute"
	ToolActionExecute    = "tool_action_execute"

	stubToolboxID = "tbx_test_snapshot"
)

// New creates an MCP server with the initial Toolbox MCP surface.
//
// TODO: The handlers are intentionally stubbed for now. They exist to validate the
// outside-in MCP contract before real codemode wiring is added.
func New() *server.MCPServer {
	mcpServer := server.NewMCPServer(
		"toolbox-mcp-server",
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

func handleDiscoveryExecute(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString("code")
	if err != nil {
		return nil, err
	}

	structured := map[string]any{
		"mode":      "discovery",
		"toolboxID": stubToolboxID,
		"result": map[string]any{
			"receivedCode": code,
			"capabilities": []map[string]any{
				{
					"name":        ToolDiscoveryExecute,
					"description": "Discover available capabilities and obtain a toolbox snapshot handle.",
				},
				{
					"name":        ToolActionExecute,
					"description": "Execute action code against a discovered toolbox snapshot.",
				},
			},
		},
	}

	return mcp.NewToolResultStructured(
		structured,
		fmt.Sprintf("discovery complete; toolboxID=%s", stubToolboxID),
	), nil
}

func handleActionExecute(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	toolboxID, err := request.RequireString("toolboxID")
	if err != nil {
		return nil, err
	}

	code, err := request.RequireString("code")
	if err != nil {
		return nil, err
	}

	structured := map[string]any{
		"mode":      "action",
		"toolboxID": toolboxID,
		"result": map[string]any{
			"receivedCode": code,
			"status":       "stub-executed",
		},
	}

	return mcp.NewToolResultStructured(
		structured,
		fmt.Sprintf("action complete for toolboxID=%s", toolboxID),
	), nil
}
