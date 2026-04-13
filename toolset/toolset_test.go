package toolset

import (
	"context"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestPreparedTool_PackageCredentialPolicy(t *testing.T) {
	t.Parallel()

	baseTool := assembler.LoadedTool{
		Name:        "pkg.tool",
		Description: "test tool",
		PackageMeta: &tooldef.Package{
			Module:  tooldef.ModulePath("example.com/pkg"),
			Name:    "pkg",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
		},
	}

	t.Run("DenyByDefault", func(t *testing.T) {
		prepared := NewPreparedToolset([]assembler.LoadedTool{baseTool})
		tool, ok := prepared.Tool("pkg.tool")
		if !ok {
			t.Fatal("expected prepared tool")
		}
		if tool.Injector() != nil {
			t.Fatal("expected nil Injector on default PreparedTool")
		}
		if tool.Allowlist() == nil {
			t.Fatal("expected non-nil Allowlist on default PreparedTool")
		}
		if tool.Allowlist().Allows("example.com") {
			t.Fatal("default PreparedTool allowlist should deny all hosts")
		}
	})

	t.Run("FallsBackToPackageAllowedHosts", func(t *testing.T) {
		toolWithHosts := baseTool
		toolWithHosts.PackageMeta = &tooldef.Package{
			Module:       tooldef.ModulePath("example.com/pkg"),
			Name:         "pkg",
			Runtime:      tooldef.RuntimeTypeScriptSandbox,
			AllowedHosts: []string{"api.example.com"},
		}

		prepared := NewPreparedToolset([]assembler.LoadedTool{toolWithHosts})
		tool, ok := prepared.Tool("pkg.tool")
		if !ok {
			t.Fatal("expected prepared tool")
		}
		if tool.Allowlist() == nil {
			t.Fatal("expected package allowlist to be materialized")
		}
		if !tool.Allowlist().Allows("api.example.com") {
			t.Fatal("expected package allowlist to permit declared host")
		}
	})

	t.Run("CredentialPolicySource", func(t *testing.T) {
		allowlist := transport.NewHostAllowlist([]string{"example.com"})
		injector := transport.NewCredentialInjector(nil, nil)

		prepared, err := PrepareTools(context.Background(), []assembler.LoadedTool{baseTool}, Config{
			CredentialPolicySource: staticPolicySource{
				baseTool.PackageMeta.Module: {
					Injector:  injector,
					Allowlist: allowlist,
				},
			},
		})
		if err != nil {
			t.Fatalf("PrepareTools error: %v", err)
		}
		tool, ok := prepared.Tool("pkg.tool")
		if !ok {
			t.Fatal("expected prepared tool")
		}
		if tool.Injector() != injector {
			t.Fatal("tool Injector() did not return the override policy value")
		}
		if tool.Allowlist() != allowlist {
			t.Fatal("tool Allowlist() did not return the override policy value")
		}
	})
}

type staticPolicySource map[tooldef.ModulePath]PackageCredentialPolicy

func (s staticPolicySource) PackageCredentialPolicy(_ context.Context, pkg tooldef.Package) (PackageCredentialPolicy, error) {
	if policy, ok := s[pkg.Module]; ok {
		return policy, nil
	}
	return PackageCredentialPolicy{}, nil
}
