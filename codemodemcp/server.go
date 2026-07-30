package codemodemcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	ToolSuperTool               = codemodesession.SuperToolName
	ToolAwaitSuperToolApprovals = codemodesession.AwaitSuperToolApprovalsName
	ToolNewSession              = codemodesession.NewSessionToolName
	defaultServerName           = "toolbox"
)

// New creates an unlocked codemode MCP server.
func New(cfgs ...codemodesession.SessionConfig) *server.MCPServer {
	return NewNamed(defaultServerName, cfgs...)
}

// NewNamed creates an unlocked codemode MCP server.
func NewNamed(name string, cfgs ...codemodesession.SessionConfig) *server.MCPServer {
	cfg := firstSessionConfig(cfgs)
	managed := &ManagedServer{
		server:  newMCPServer(name),
		manager: codemodesession.NewUnlockedManager(currentWorkingDir(), cfg),
	}
	managed.SetPreparedTools(cfg.PreparedTools)
	return managed.server
}

type ManagedServer struct {
	server      *server.MCPServer
	manager     *codemodesession.Manager
	afterChange func()
}

func OpenManagedNamed(_ context.Context, name, currentDir string, cfgs ...codemodesession.SessionConfig) (*ManagedServer, error) {
	cfg := firstSessionConfig(cfgs)
	if strings.TrimSpace(currentDir) == "" {
		currentDir = currentWorkingDir()
	}
	managed := &ManagedServer{
		server:  newMCPServer(name),
		manager: codemodesession.NewUnlockedManager(currentDir, cfg),
	}
	managed.SetPreparedTools(cfg.PreparedTools)
	return managed, nil
}

func OpenManagedBoundNamed(ctx context.Context, name, currentDir, tbSession string, cfgs ...codemodesession.SessionConfig) (*ManagedServer, error) {
	cfg := firstSessionConfig(cfgs)
	if strings.TrimSpace(currentDir) == "" {
		currentDir = currentWorkingDir()
	}
	manager, err := codemodesession.OpenLockedManager(ctx, tbSession, currentDir, cfg)
	if err != nil {
		return nil, err
	}
	managed := &ManagedServer{
		server:  newMCPServer(name),
		manager: manager,
	}
	managed.SetPreparedTools(cfg.PreparedTools)
	return managed, nil
}

func newMCPServer(name string) *server.MCPServer {
	if strings.TrimSpace(name) == "" {
		name = defaultServerName
	}
	return server.NewMCPServer(
		name,
		"0.1.0",
		server.WithToolCapabilities(true),
	)
}

func (s *ManagedServer) Server() *server.MCPServer {
	if s == nil {
		return nil
	}
	return s.server
}

func (s *ManagedServer) Close() error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Close()
}

func (s *ManagedServer) SetPreparedTools(prepared toolset.PreparedToolset) {
	if s == nil || s.server == nil || s.manager == nil {
		return
	}
	s.manager.SetPreparedTools(prepared)
	s.server.SetTools(s.tools(prepared)...)
}

func (s *ManagedServer) SetAfterChange(fn func()) {
	if s == nil {
		return
	}
	s.afterChange = fn
}

func (s *ManagedServer) SetAfterSubmit(fn func()) {
	s.SetAfterChange(fn)
}

func (s *ManagedServer) PendingApprovals(ctx context.Context) ([]codemodesession.PendingApproval, error) {
	if s == nil || s.manager == nil {
		return nil, nil
	}
	return s.manager.PendingApprovals(ctx)
}

func (s *ManagedServer) Locked() bool {
	if s == nil || s.manager == nil {
		return false
	}
	return s.manager.Locked()
}

func (s *ManagedServer) BoundTBSession() string {
	if s == nil || s.manager == nil {
		return ""
	}
	return s.manager.BoundTBSession()
}

func (s *ManagedServer) ApplyApprovals(ctx context.Context, decisions []codemodesession.ApprovalDecision) error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.ApplyApprovals(ctx, decisions)
}

func (s *ManagedServer) tools(prepared toolset.PreparedToolset) []server.ServerTool {
	if s == nil || s.manager == nil {
		return nil
	}
	tools := []server.ServerTool{{
		Tool:    s.newSuperTool(),
		Handler: s.handleSuperTool,
	}}
	if !s.manager.Locked() {
		tools = append(tools, server.ServerTool{
			Tool:    s.newSessionTool(prepared.HasApprovalTools()),
			Handler: s.handleNewSession,
		})
	}
	if prepared.HasApprovalTools() {
		tools = append(tools, server.ServerTool{
			Tool:    s.newAwaitSuperToolApprovalsTool(),
			Handler: s.handleAwaitSuperToolApprovals,
		})
	}
	return tools
}

func (s *ManagedServer) newSuperTool() mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription(s.manager.SuperToolDescription()),
		mcp.WithString(codemodesession.TypeScriptCellSourceParam, mcp.Required(), mcp.Description("TypeScript code (can be multiline) for next cell.")),
		mcp.WithNumber(codemodesession.TimeoutSecsParam,
			mcp.Description(fmt.Sprintf("Optional. Maximum seconds to allow this cell to run before it fails. Use a larger value for long-running network or tool-heavy work. Defaults to %g.", codemodesession.DefaultSubmitTimeout.Seconds())),
			mcp.Min(0.001),
			mcp.DefaultNumber(codemodesession.DefaultSubmitTimeout.Seconds()),
		),
	}
	if !s.manager.Locked() {
		options = append(options, mcp.WithString(codemodesession.TBSessionParam, mcp.Required(), mcp.Description("Notebook identity. Reuse the same tb_session to continue the same notebook.")))
	}
	return mcp.NewTool(ToolSuperTool, options...)
}

func (s *ManagedServer) newAwaitSuperToolApprovalsTool() mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription("Wait for the outstanding approval(s) to be handled. Returns immediately with (no outstanding approvals). when nothing is waiting. Get as many approvals done as possible before calling, then call again if more are still waiting."),
	}
	if !s.manager.Locked() {
		options = append(options, mcp.WithString(codemodesession.TBSessionParam, mcp.Required(), mcp.Description("Notebook identity. Reuse the same tb_session to continue the same notebook.")))
	}
	return mcp.NewTool(ToolAwaitSuperToolApprovals, options...)
}

func (s *ManagedServer) newSessionTool(awaitAvailable bool) mcp.Tool {
	return mcp.NewTool(
		ToolNewSession,
		mcp.WithDescription(codemodesession.NewSessionToolDescription(awaitAvailable)),
		mcp.WithString(codemodesession.IntentParam, mcp.Required(), mcp.Description("User-facing task intent for this notebook. This appears as the approval-console context.")),
	)
}

func (s *ManagedServer) handleSuperTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := request.RequireString(codemodesession.TypeScriptCellSourceParam)
	if err != nil {
		return nil, err
	}
	tbSession := ""
	if s.manager != nil && !s.manager.Locked() {
		tbSession, err = request.RequireString(codemodesession.TBSessionParam)
		if err != nil {
			return nil, err
		}
	}
	submitCtx, cancel, err := withSuperToolTimeout(ctx, request)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := s.manager.Submit(submitCtx, tbSession, code)
	if err != nil {
		return nil, err
	}
	s.notifyChange()
	return mcp.NewToolResultText(result), nil
}

func (s *ManagedServer) handleAwaitSuperToolApprovals(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tbSession := ""
	var err error
	if s.manager != nil && !s.manager.Locked() {
		tbSession, err = request.RequireString(codemodesession.TBSessionParam)
		if err != nil {
			return nil, err
		}
	}
	if s.manager != nil {
		if err := s.manager.EnsureSession(ctx, tbSession); err != nil {
			return nil, err
		}
		s.notifyChange()
	}
	result, err := s.manager.AwaitNextApproval(ctx, tbSession)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(result.Text()), nil
}

func (s *ManagedServer) handleNewSession(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	intent, err := request.RequireString(codemodesession.IntentParam)
	if err != nil {
		return nil, err
	}
	tbSession, err := s.manager.CreateFreshSession(ctx, intent)
	if err != nil {
		return nil, err
	}
	s.notifyChange()
	return mcp.NewToolResultText(s.manager.NewSessionResult(tbSession)), nil
}

func (s *ManagedServer) notifyChange() {
	if s == nil || s.afterChange == nil {
		return
	}
	s.afterChange()
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

func withSuperToolTimeout(ctx context.Context, request mcp.CallToolRequest) (context.Context, context.CancelFunc, error) {
	timeout, err := superToolTimeout(request)
	if err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	next, cancel := context.WithTimeout(ctx, timeout)
	return next, cancel, nil
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
