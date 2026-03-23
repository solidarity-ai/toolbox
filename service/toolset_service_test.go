package service_test

import (
	"context"
	"testing"

	"github.com/solidarity-ai/toolbox/service"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestToolsetServiceDiscoveryReturnsAgentViewTools(t *testing.T) {
	resolved := tooltest.CalcToolset(t)
	svc := service.NewToolsetService(resolved)

	result, err := svc.ExecuteDiscovery(context.Background())
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}

	if result.IsError {
		t.Fatal("expected non-error result")
	}

	// Should contain tools from the calc package.
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatal("expected structured content to be a map")
	}

	toolsRaw, ok := structured["tools"]
	if !ok {
		t.Fatal("expected tools in structured content")
	}

	toolList, ok := toolsRaw.([]any)
	if !ok {
		t.Fatalf("expected tools to be a list, got %T", toolsRaw)
	}

	if len(toolList) != 3 {
		t.Fatalf("expected 3 tools from calc package, got %d", len(toolList))
	}
}

func TestToolsetServiceExecuteRunsCodemode(t *testing.T) {
	resolved := tooltest.CalcToolset(t)
	svc := service.NewToolsetService(resolved)

	result, err := svc.ExecuteAction(context.Background(), `
export default tools.calc.add({ a: 5, b: 3 });
`)
	if err != nil {
		t.Fatalf("action: %v", err)
	}

	if result.IsError {
		t.Fatal("expected non-error result")
	}

	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatal("expected structured content to be a map")
	}

	if got := structured["result"]; got != "8" {
		t.Fatalf("expected result 8, got %v", got)
	}
}
