package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

// New creates an MCP server that exposes one MCP tool per visible invoke tool.
func New(resolved toolset.ResolvedToolset) *server.MCPServer {
	mcpServer := server.NewMCPServer(
		"toolbox-mcp-server",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	for _, tool := range resolved.Tools() {
		mcpTool := mcp.NewTool(
			tool.Name,
			mcp.WithDescription(tool.Description),
		)

		mcpServer.AddTool(mcpTool, handleToolCall(resolved, tool.Name))
	}

	return mcpServer
}

func handleToolCall(resolved toolset.ResolvedToolset, toolName string) server.ToolHandlerFunc {
	return func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ran, err := invoke.Run(resolved, toolName, argumentMap(request.Params.Arguments))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		structured := map[string]any{
			"tool":   toolName,
			"result": ran,
			"status": "stub-invoked",
		}

		return mcp.NewToolResultStructured(
			structured,
			fmt.Sprintf("invoked %s", toolName),
		), nil
	}
}

func argumentMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if args, ok := value.(map[string]any); ok {
		return args
	}
	return nil
}
