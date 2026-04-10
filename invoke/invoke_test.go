package invoke_test

import (
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
