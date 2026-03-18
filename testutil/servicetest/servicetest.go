package servicetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/solidarity-ai/toolbox/service"
)

type envContextKey struct{}

// ContextWithEnv attaches test env values to a context.
func ContextWithEnv(ctx context.Context, env map[string]string) context.Context {
	if env == nil {
		env = map[string]string{}
	}
	return context.WithValue(ctx, envContextKey{}, cloneEnv(env))
}

// EnvFromContext retrieves test env values from a context.
func EnvFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return map[string]string{}
	}
	if env, ok := ctx.Value(envContextKey{}).(map[string]string); ok {
		return cloneEnv(env)
	}
	return map[string]string{}
}

func ExecuteToolDiscovery(t testing.TB, env map[string]string, req service.ToolDiscoveryExecuteRequest) *mcp.CallToolResult {
	t.Helper()

	result, err := service.ExecuteToolDiscovery(ContextWithEnv(context.Background(), env), req)
	if err != nil {
		t.Fatalf("execute tool discovery: %v", err)
	}
	return result
}

func ExecuteToolAction(t testing.TB, env map[string]string, req service.ToolActionExecuteRequest) *mcp.CallToolResult {
	t.Helper()

	result, err := service.ExecuteToolAction(ContextWithEnv(context.Background(), env), req)
	if err != nil {
		t.Fatalf("execute tool action: %v", err)
	}
	return result
}

func StructuredMap(t testing.TB, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	if result == nil {
		t.Fatalf("result is nil")
	}
	if result.StructuredContent == nil {
		t.Fatalf("structured content is nil")
	}

	if m, ok := result.StructuredContent.(map[string]any); ok {
		return m
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}

	return out
}

func cloneEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}
