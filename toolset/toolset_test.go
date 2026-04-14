package toolset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/secrets"
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

	t.Run("LockedCredentialPolicySourceKeepsToolUnavailable", func(t *testing.T) {
		lockedTool := assembler.LoadedTool{
			Name:        "locked.tool",
			Description: "locked tool",
			PackageMeta: &tooldef.Package{
				Module:  tooldef.ModulePath("example.com/locked"),
				Name:    "locked",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
			},
		}
		openTool := assembler.LoadedTool{
			Name:        "open.tool",
			Description: "open tool",
			PackageMeta: &tooldef.Package{
				Module:  tooldef.ModulePath("example.com/open"),
				Name:    "open",
				Runtime: tooldef.RuntimeTypeScriptSandbox,
			},
		}

		prepared, err := PrepareTools(context.Background(), []assembler.LoadedTool{lockedTool, openTool}, Config{
			CredentialPolicySource: errPolicySource{
				errs: map[tooldef.ModulePath]error{
					lockedTool.PackageMeta.Module: secrets.ErrLocked,
				},
			},
		})
		if err != nil {
			t.Fatalf("PrepareTools error: %v", err)
		}
		lockedPrepared, ok := prepared.Tool("locked.tool")
		if !ok {
			t.Fatal("locked tool should still be prepared")
		}
		if !lockedPrepared.Unavailable() {
			t.Fatal("locked tool should be unavailable")
		}
		if lockedPrepared.UnavailableReason() != ToolUnavailableReasonSecretStoreLocked {
			t.Fatalf("locked reason = %q, want %q", lockedPrepared.UnavailableReason(), ToolUnavailableReasonSecretStoreLocked)
		}
		if _, err := lockedPrepared.ValidateCall(nil); err == nil {
			t.Fatal("ValidateCall() error = nil, want unavailable error")
		} else if !strings.Contains(err.Error(), "secret store is locked") {
			t.Fatalf("ValidateCall() error = %v, want locked message", err)
		}
		if _, ok := prepared.Tool("open.tool"); !ok {
			t.Fatal("open tool should still be prepared")
		}
		omitted := prepared.OmittedPackages()
		if len(omitted) != 1 {
			t.Fatalf("omitted len = %d, want 1", len(omitted))
		}
		if omitted[0].Name != "locked" {
			t.Fatalf("omitted[0].Name = %q, want locked", omitted[0].Name)
		}
		if omitted[0].Reason != OmittedPackageReasonSecretStoreLocked {
			t.Fatalf("omitted[0].Reason = %q, want %q", omitted[0].Reason, OmittedPackageReasonSecretStoreLocked)
		}
	})

	t.Run("OtherCredentialPolicyErrorsStillFail", func(t *testing.T) {
		_, err := PrepareTools(context.Background(), []assembler.LoadedTool{baseTool}, Config{
			CredentialPolicySource: errPolicySource{
				errs: map[tooldef.ModulePath]error{
					baseTool.PackageMeta.Module: errors.New("boom"),
				},
			},
		})
		if err == nil {
			t.Fatal("PrepareTools error = nil, want error")
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

type errPolicySource struct {
	errs map[tooldef.ModulePath]error
}

func (s errPolicySource) PackageCredentialPolicy(_ context.Context, pkg tooldef.Package) (PackageCredentialPolicy, error) {
	if err, ok := s.errs[pkg.Module]; ok {
		return PackageCredentialPolicy{}, err
	}
	return PackageCredentialPolicy{}, nil
}
