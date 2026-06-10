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
	mcpServer := newServer(name)
	mcpServer.SetTools(serverToolsFromPrepared(prepared)...)
	return mcpServer
}

type ManagedServer struct {
	server *server.MCPServer
}

func NewManagedNamed(name string) *ManagedServer {
	return &ManagedServer{server: newServer(name)}
}

func (s *ManagedServer) Server() *server.MCPServer {
	if s == nil {
		return nil
	}
	return s.server
}

func (s *ManagedServer) SetPreparedTools(prepared toolset.PreparedToolset) {
	if s == nil || s.server == nil {
		return
	}
	s.server.SetTools(serverToolsFromPrepared(prepared)...)
}

func newServer(name string) *server.MCPServer {
	if strings.TrimSpace(name) == "" {
		name = defaultServerName
	}

	return server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(true),
	)
}

func serverToolsFromPrepared(prepared toolset.PreparedToolset) []server.ServerTool {
	view := prepared.AgentView()
	tools := make([]server.ServerTool, 0, len(view.Tools))
	for _, at := range view.Tools {
		tools = append(tools, server.ServerTool{
			Tool:    newMCPTool(at),
			Handler: handleToolCall(prepared, at.Name),
		})
	}
	return tools
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
		args, err := invoke.EncodeInvokeArgs(argumentMap(request.Params.Arguments))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		ran, err := invoke.RunContext(ctx, prepared, toolName, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		text, err := invoke.DecodeWireString(ran)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(text), nil
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
