package invoke_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/secrets"
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

func TestRunUnavailableToolReturnsTypedError(t *testing.T) {
	pkg := tooldef.Package{
		Module:  tooldef.ModulePath("example.com/locked"),
		Name:    "locked",
		Runtime: tooldef.RuntimeTypeScriptSandbox,
	}

	prepared, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{
		{
			Name:        "locked.noop",
			Description: "Locked tool",
			PackageMeta: &pkg,
		},
	}, toolset.Config{
		CredentialPolicySource: invokeErrPolicySource{
			pkg.Module: secrets.ErrLocked,
		},
	})
	if err != nil {
		t.Fatalf("PrepareTools() error: %v", err)
	}

	_, err = invoke.Run(prepared, "locked.noop", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want unavailable error")
	}

	var unavailable *toolset.ToolUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Run() error = %v, want ToolUnavailableError", err)
	}
	if unavailable.Reason != toolset.ToolUnavailableReasonSecretStoreLocked {
		t.Fatalf("Reason = %q, want %q", unavailable.Reason, toolset.ToolUnavailableReasonSecretStoreLocked)
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

type invokeErrPolicySource map[tooldef.ModulePath]error

func (s invokeErrPolicySource) PackageCredentialPolicy(_ context.Context, pkg tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	if err, ok := s[pkg.Module]; ok {
		return toolset.PackageCredentialPolicy{}, err
	}
	return toolset.PackageCredentialPolicy{}, nil
}
