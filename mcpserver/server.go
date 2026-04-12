package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

const defaultServerName = "toolbox"

// New creates an MCP server that exposes one MCP tool per visible invoke tool.
func New(prepared toolset.PreparedToolset) *server.MCPServer {
	return NewNamed(defaultServerName, prepared)
}

// NewNamed creates an MCP server that exposes one MCP tool per visible invoke tool.
func NewNamed(name string, prepared toolset.PreparedToolset) *server.MCPServer {
	if strings.TrimSpace(name) == "" {
		name = defaultServerName
	}

	mcpServer := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	view := prepared.AgentView()
	for _, at := range view.Tools {
		mcpTool := newMCPTool(at)
		mcpServer.AddTool(mcpTool, handleToolCall(prepared, at.Name))
	}

	return mcpServer
}

func newMCPTool(at toolset.AgentTool) mcp.Tool {
	schema := at.ParamsSchema

	if len(schema) == 0 {
		return mcp.NewTool(
			at.Name,
			mcp.WithDescription(at.Description),
		)
	}

	rawSchema, err := json.Marshal(schema)
	if err != nil {
		return mcp.NewTool(
			at.Name,
			mcp.WithDescription(at.Description),
		)
	}

	return mcp.NewToolWithRawSchema(at.Name, at.Description, rawSchema)
}

func handleToolCall(prepared toolset.PreparedToolset, toolName string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ran, err := invoke.RunContext(ctx, prepared, toolName, argumentMap(request.Params.Arguments))
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
