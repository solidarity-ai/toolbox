package invoke_test

import (
	"context"
	"testing"

	"github.com/dop251/goja"
	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

func invokeArgs(t testing.TB, args map[string]any) jswire.Value {
	t.Helper()
	if args == nil {
		return nil
	}
	raw, err := jswire.EncodeGoja(goja.New().ToValue(args))
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return raw
}

func runInvokeString(t testing.TB, prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	t.Helper()
	raw, err := invoke.Run(prepared, toolName, invokeArgs(t, args))
	if err != nil {
		return "", err
	}
	return invokeWireString(t, raw), nil
}

func runInvokeContextString(t testing.TB, ctx context.Context, prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	t.Helper()
	raw, err := invoke.RunContext(ctx, prepared, toolName, invokeArgs(t, args))
	if err != nil {
		return "", err
	}
	return invokeWireString(t, raw), nil
}

func invokeWireString(t testing.TB, raw jswire.Value) string {
	t.Helper()
	value, err := jswire.DecodeGoja(goja.New(), raw)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return value.String()
}
