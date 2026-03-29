package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
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
		mcpTool := newMCPTool(tool)
		mcpServer.AddTool(mcpTool, handleToolCall(resolved, tool.Name))
	}

	return mcpServer
}

func newMCPTool(tool tooldef.ResolvedTool) mcp.Tool {
	if len(tool.ParamsSchema) == 0 {
		return mcp.NewTool(
			tool.Name,
			mcp.WithDescription(tool.Description),
		)
	}

	rawSchema, err := json.Marshal(tool.ParamsSchema)
	if err != nil {
		return mcp.NewTool(
			tool.Name,
			mcp.WithDescription(tool.Description),
		)
	}

	return mcp.NewToolWithRawSchema(tool.Name, tool.Description, rawSchema)
}

func handleToolCall(resolved toolset.ResolvedToolset, toolName string) server.ToolHandlerFunc {
	return func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ran, err := invoke.Run(resolved, toolName, argumentMap(request.Params.Arguments))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(ran), nil
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
