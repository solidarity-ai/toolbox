package codemodemcp

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodesession"
)

const (
	ToolSuperTool     = codemodesession.SuperToolName
	defaultServerName = "toolbox"
)

// New creates an MCP server with the initial Toolbox MCP surface.
func New(cfgs ...codemodesession.SessionConfig) *server.MCPServer {
	return NewNamed(defaultServerName, cfgs...)
}

// NewNamed creates an MCP server with the initial Toolbox MCP surface.
func NewNamed(name string, cfgs ...codemodesession.SessionConfig) *server.MCPServer {
	cfg := firstSessionConfig(cfgs)
	metaSession := &codemodesession.Session{}
	metaSession.SetPreparedTools(cfg.PreparedTools)
	instructions := metaSession.Instructions()
	runner := &sessionRunner{currentDir: currentWorkingDir(), config: cfg}
	if strings.TrimSpace(name) == "" {
		name = defaultServerName
	}

	mcpServer := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	mcpServer.AddTool(newSuperTool(instructions), runner.handleSuperTool)

	return mcpServer
}

func newSuperTool(instructions string) mcp.Tool {
	return mcp.NewTool(
		ToolSuperTool,
		mcp.WithDescription(instructions),
		mcp.WithString(codemodesession.TypeScriptCellSourceParam, mcp.Required(), mcp.Description("TypeScript code (can be multiline) for next cell.")),
	)
}

type sessionRunner struct {
	mu         sync.Mutex
	session    *codemodesession.Session
	currentDir string
	config     codemodesession.SessionConfig
}

func (r *sessionRunner) handleSuperTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString(codemodesession.TypeScriptCellSourceParam)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(r.submit(ctx, code)), nil
}

func (r *sessionRunner) submit(ctx context.Context, code string) string {
	session, err := r.open(ctx)
	if err != nil {
		return "cell (failed to commit)\n==\nfailure: " + strings.TrimSpace(err.Error()) + "\n"
	}
	return session.Submit(ctx, code)
}

func (r *sessionRunner) open(ctx context.Context) (*codemodesession.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.session != nil {
		return r.session, nil
	}
	session, err := codemodesession.OpenMemory(ctx, r.currentDir, r.config)
	if err != nil {
		return nil, err
	}
	r.session = session
	return r.session, nil
}

func firstSessionConfig(cfgs []codemodesession.SessionConfig) codemodesession.SessionConfig {
	if len(cfgs) == 0 {
		return codemodesession.SessionConfig{}
	}
	return cfgs[0]
}

func currentWorkingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
