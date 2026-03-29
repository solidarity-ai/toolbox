package codemodemcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ToolDiscoveryExecute = "tool_discovery_execute"
	ToolActionExecute    = "tool_action_execute"
)

// New creates an MCP server with the initial Toolbox MCP surface.
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

func handleDiscoveryExecute(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString("code")
	if err != nil {
		return nil, err
	}

	// Stub: return a fake discovery result.
	result := map[string]any{
		"mode":         "discovery",
		"toolboxID":    "tbx_stub",
		"receivedCode": code,
	}
	return toToolResult(result)
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

	// Stub: return a fake action result.
	result := map[string]any{
		"mode":         "action",
		"toolboxID":    toolboxID,
		"receivedCode": code,
	}
	return toToolResult(result)
}

// toToolResult wraps any value into an MCP text result.
// Strings pass through; everything else is JSON-serialized.
func toToolResult(v any) (*mcp.CallToolResult, error) {
	if s, ok := v.(string); ok {
		return mcp.NewToolResultText(s), nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return mcp.NewToolResultText(string(data)), nil
}
