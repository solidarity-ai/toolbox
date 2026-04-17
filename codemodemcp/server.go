package codemodemcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	ToolSuperTool               = codemodesession.SuperToolName
	ToolAwaitSuperToolApprovals = codemodesession.AwaitSuperToolApprovalsName
	defaultServerName           = "toolbox"
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

	mcpServer.SetTools(sessionBackedServerTools(cfg.PreparedTools.HasApprovalTools(), newSuperTool(instructions), runner.handleSuperTool, newAwaitSuperToolApprovalsTool(), runner.handleAwaitSuperToolApprovals)...)

	return mcpServer
}

type ManagedServer struct {
	server      *server.MCPServer
	session     *codemodesession.Session
	afterSubmit func()
}

func OpenManagedNamed(ctx context.Context, name, currentDir string) (*ManagedServer, error) {
	if strings.TrimSpace(currentDir) == "" {
		currentDir = currentWorkingDir()
	}
	if strings.TrimSpace(name) == "" {
		name = defaultServerName
	}

	session, err := codemodesession.OpenSQLite(ctx, managedSessionSQLitePath(currentDir, name), currentDir)
	if err != nil {
		return nil, err
	}

	mcpServer := server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(true),
	)
	managed := &ManagedServer{
		server:  mcpServer,
		session: session,
	}
	managed.SetPreparedTools(toolset.PreparedToolset{})
	return managed, nil
}

func (s *ManagedServer) Server() *server.MCPServer {
	if s == nil {
		return nil
	}
	return s.server
}

func (s *ManagedServer) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close()
}

func (s *ManagedServer) SetPreparedTools(prepared toolset.PreparedToolset) {
	if s == nil || s.server == nil || s.session == nil {
		return
	}
	s.session.SetPreparedTools(prepared)
	s.server.SetTools(sessionBackedServerTools(prepared.HasApprovalTools(), newSuperTool(s.session.Instructions()), s.handleSuperTool, newAwaitSuperToolApprovalsTool(), s.handleAwaitSuperToolApprovals)...)
}

func (s *ManagedServer) handleSuperTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString(codemodesession.TypeScriptCellSourceParam)
	if err != nil {
		return nil, err
	}
	submitCtx, cancel, err := withSuperToolTimeout(ctx, request)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result := s.session.Submit(submitCtx, code)
	if s.afterSubmit != nil {
		s.afterSubmit()
	}
	return mcp.NewToolResultText(result), nil
}

func (s *ManagedServer) handleAwaitSuperToolApprovals(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := s.session.AwaitNextApproval(ctx)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(result.Text()), nil
}

func (s *ManagedServer) SetAfterSubmit(fn func()) {
	if s == nil {
		return
	}
	s.afterSubmit = fn
}

func (s *ManagedServer) PendingApprovals(ctx context.Context) ([]codemodesession.PendingApproval, error) {
	if s == nil || s.session == nil {
		return nil, nil
	}
	return s.session.PendingApprovals(ctx)
}

func (s *ManagedServer) ApplyApprovals(ctx context.Context, decisions []codemodesession.ApprovalDecision) error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.ApplyApprovals(ctx, decisions)
}

func newSuperTool(instructions string) mcp.Tool {
	return mcp.NewTool(
		ToolSuperTool,
		mcp.WithDescription(instructions),
		mcp.WithString(codemodesession.TypeScriptCellSourceParam, mcp.Required(), mcp.Description("TypeScript code (can be multiline) for next cell.")),
		mcp.WithNumber(codemodesession.TimeoutSecsParam,
			mcp.Description(fmt.Sprintf("Optional. Maximum seconds to allow this cell to run before it fails. Use a larger value for long-running network or tool-heavy work. Defaults to %g.", codemodesession.DefaultSubmitTimeout.Seconds())),
			mcp.Min(0.001),
			mcp.DefaultNumber(codemodesession.DefaultSubmitTimeout.Seconds()),
		),
	)
}

func newAwaitSuperToolApprovalsTool() mcp.Tool {
	return mcp.NewTool(
		ToolAwaitSuperToolApprovals,
		mcp.WithDescription("Wait for the outstanding approval(s) to be handled. Returns immediately with (no outstanding approvals). when nothing is waiting. Get as many approvals done as possible before calling, then call again if more are still waiting."),
	)
}

func sessionBackedServerTools(hasApprovalTools bool, superTool mcp.Tool, superHandler server.ToolHandlerFunc, awaitTool mcp.Tool, awaitHandler server.ToolHandlerFunc) []server.ServerTool {
	tools := []server.ServerTool{{
		Tool:    superTool,
		Handler: superHandler,
	}}
	if hasApprovalTools {
		tools = append(tools, server.ServerTool{
			Tool:    awaitTool,
			Handler: awaitHandler,
		})
	}
	return tools
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
	submitCtx, cancel, err := withSuperToolTimeout(ctx, request)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return mcp.NewToolResultText(r.submit(submitCtx, code)), nil
}

func (r *sessionRunner) handleAwaitSuperToolApprovals(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	session, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	result, err := session.AwaitNextApproval(ctx)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(result.Text()), nil
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

func managedSessionSQLitePath(currentDir, name string) string {
	safe := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(strings.TrimSpace(name))
	if safe == "" {
		safe = defaultServerName
	}
	return filepath.Join(currentDir, ".toolbox-codemodemcp-"+safe+".sqlite")
}

func withSuperToolTimeout(ctx context.Context, request mcp.CallToolRequest) (context.Context, context.CancelFunc, error) {
	timeout, err := superToolTimeout(request)
	if err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nextCtx, cancel := context.WithTimeout(ctx, timeout)
	return nextCtx, cancel, nil
}

func superToolTimeout(request mcp.CallToolRequest) (time.Duration, error) {
	args := request.GetArguments()
	if _, ok := args[codemodesession.TimeoutSecsParam]; !ok {
		return codemodesession.DefaultSubmitTimeout, nil
	}
	seconds, err := request.RequireFloat(codemodesession.TimeoutSecsParam)
	if err != nil {
		return 0, err
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", codemodesession.TimeoutSecsParam)
	}
	timeout := time.Duration(seconds * float64(time.Second))
	if timeout <= 0 {
		return 0, fmt.Errorf("%s is too small", codemodesession.TimeoutSecsParam)
	}
	return timeout, nil
}
