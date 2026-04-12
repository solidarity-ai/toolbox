package invoke_test

import (
	"context"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestRunUnknownToolErrors(t *testing.T) {
	prepared := toolset.NewPreparedToolset(nil)

	_, err := invoke.Run(prepared, "does.not.exist", nil)
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestRunVisibleToolWithoutExecutableErrors(t *testing.T) {
	pkg := tooldef.Package{
		Name:    "broken",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
	}
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{
			Name:        "broken.noop",
			Description: "Broken tool",
			PackageMeta: &pkg,
		},
	})

	_, err := invoke.Run(prepared, "broken.noop", nil)
	if err == nil {
		t.Fatal("expected missing executable error")
	}
	if !strings.Contains(err.Error(), "no executable") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestRunContextExecutesBuiltInTools(t *testing.T) {
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{{
		Name: "builtin.echo",
		BuiltIn: func(ctx context.Context, args map[string]any) (string, error) {
			if ctx == nil {
				t.Fatal("BuiltIn ctx = nil")
			}
			if got := args["value"]; got != "ok" {
				t.Fatalf("args[value] = %#v, want %q", got, "ok")
			}
			return "done", nil
		},
	}})

	got, err := invoke.RunContext(context.Background(), prepared, "builtin.echo", map[string]any{"value": "ok"})
	if err != nil {
		t.Fatalf("RunContext() error: %v", err)
	}
	if got != "done" {
		t.Fatalf("RunContext() = %q, want %q", got, "done")
	}
}
