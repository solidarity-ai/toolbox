package service

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/toolset"
)

// Service provides the toolset API surface backed by a resolved toolset.
type Service struct {
	resolved toolset.ResolvedToolset
}

// NewService creates a service from a resolved toolset.
func NewService(resolved toolset.ResolvedToolset) *Service {
	return &Service{resolved: resolved}
}

// DiscoverTools returns the agent-visible tool surface from the resolved toolset.
func (s *Service) DiscoverTools(_ context.Context) (toolset.AgentView, error) {
	return s.resolved.AgentView(), nil
}

const (
	ToolDiscoveryExecute = "tool_discovery_execute"
	ToolActionExecute    = "tool_action_execute"

	stubToolboxID = "tbx_test_snapshot"
)

// ToolDiscoveryExecuteRequest is the canonical request shape for discovery-oriented code execution.
type ToolDiscoveryExecuteRequest struct {
	Code string
}

// ToolActionExecuteRequest is the canonical request shape for action-oriented code execution.
type ToolActionExecuteRequest struct {
	ToolboxID string
	Code      string
}

// ExecuteToolDiscovery is a stub discovery implementation used to lock in caller contracts.
func ExecuteToolDiscovery(_ context.Context, request ToolDiscoveryExecuteRequest) (*mcp.CallToolResult, error) {
	structured := map[string]any{
		"mode":      "discovery",
		"toolboxID": stubToolboxID,
		"result": map[string]any{
			"receivedCode": request.Code,
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

// ExecuteToolAction is a stub action implementation used to lock in caller contracts.
func ExecuteToolAction(_ context.Context, request ToolActionExecuteRequest) (*mcp.CallToolResult, error) {
	structured := map[string]any{
		"mode":      "action",
		"toolboxID": request.ToolboxID,
		"result": map[string]any{
			"receivedCode": request.Code,
			"status":       "stub-executed",
		},
	}

	return mcp.NewToolResultStructured(
		structured,
		fmt.Sprintf("action complete for toolboxID=%s", request.ToolboxID),
	), nil
}
