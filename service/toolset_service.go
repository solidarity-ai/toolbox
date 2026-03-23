package service

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/codemode"
	"github.com/solidarity-ai/toolbox/toolset"
)

// ToolsetService wraps a ResolvedToolset to provide discovery and execution.
type ToolsetService struct {
	resolved toolset.ResolvedToolset
}

// NewToolsetService creates a service backed by the given resolved toolset.
func NewToolsetService(resolved toolset.ResolvedToolset) *ToolsetService {
	return &ToolsetService{resolved: resolved}
}

// ExecuteDiscovery returns the agent-visible tool list from the resolved toolset.
func (s *ToolsetService) ExecuteDiscovery(_ context.Context) (*mcp.CallToolResult, error) {
	view := s.resolved.AgentView()

	toolDescriptors := make([]any, len(view.Tools))
	for i, tool := range view.Tools {
		desc := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if tool.ParamsSchema != nil {
			desc["paramsSchema"] = tool.ParamsSchema
		}
		toolDescriptors[i] = desc
	}

	structured := map[string]any{
		"mode":  "discovery",
		"tools": toolDescriptors,
	}

	return mcp.NewToolResultStructured(
		structured,
		fmt.Sprintf("discovered %d tools", len(view.Tools)),
	), nil
}

// ExecuteAction runs codemode code against the resolved toolset.
func (s *ToolsetService) ExecuteAction(_ context.Context, code string) (*mcp.CallToolResult, error) {
	result, err := codemode.Run(s.resolved, code)
	if err != nil {
		return nil, fmt.Errorf("execute action: %w", err)
	}

	structured := map[string]any{
		"mode":   "action",
		"result": result,
	}

	return mcp.NewToolResultStructured(
		structured,
		fmt.Sprintf("action complete: %s", result),
	), nil
}
