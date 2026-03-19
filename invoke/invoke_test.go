package invoke_test

import (
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestRunUnknownToolErrors(t *testing.T) {
	resolved := toolset.NewResolvedToolset(nil)

	_, err := invoke.Run(resolved, "does.not.exist", nil)
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
	resolved := toolset.NewResolvedToolset([]tooldef.ResolvedTool{
		{
			Name:        "broken.noop",
			Description: "Broken tool",
			Package:     &pkg,
		},
	})

	_, err := invoke.Run(resolved, "broken.noop", nil)
	if err == nil {
		t.Fatal("expected missing executable error")
	}
	if !strings.Contains(err.Error(), "no executable") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}
