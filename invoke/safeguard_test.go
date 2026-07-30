package invoke

import (
	"context"
	"errors"
	"testing"

	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/safeguard"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

type invocationGuardFunc func(context.Context, tooldef.ModulePath, tooldef.Version) error

func (f invocationGuardFunc) CheckPackage(ctx context.Context, module tooldef.ModulePath, version tooldef.Version) error {
	return f(ctx, module, version)
}

func TestRunChecksPackageGuardImmediatelyBeforeExecution(t *testing.T) {
	executed := false
	loaded := assembler.LoadedTool{
		Name:           "example.run",
		PackageMeta:    &tooldef.Package{Module: "example.com/acme/example", Name: "example"},
		PackageVersion: "v1.0.0",
		BuiltIn: func(context.Context, map[string]any) (string, error) {
			executed = true
			return "unexpected", nil
		},
	}
	prepared, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{loaded}, toolset.Config{
		PackageGuard: invocationGuardFunc(func(context.Context, tooldef.ModulePath, tooldef.Version) error {
			return &safeguard.BlockedError{Subject: "example.com/acme/example@v1.0.0", Reason: "known vulnerable package"}
		}),
	})
	if err != nil {
		t.Fatalf("PrepareTools() error = %v", err)
	}
	args, err := jswire.Encode(jswire.ObjectType{})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	_, err = Run(prepared, "example.run", args)
	var blocked *safeguard.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Run() error = %v, want *safeguard.BlockedError", err)
	}
	if executed {
		t.Fatal("tool executed despite package revocation")
	}
}
