package tooltest

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/toolset"
)

// WireArgs encodes a map of arguments to a jswire value through a goja
// runtime, matching how arguments arrive from real JS callers.
func WireArgs(t testing.TB, args map[string]any) jswire.Value {
	t.Helper()
	raw, err := jswire.EncodeGoja(goja.New().ToValue(args))
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return raw
}

// WireString decodes a jswire value through a goja runtime and returns its
// string form.
func WireString(t testing.TB, raw jswire.Value) string {
	t.Helper()
	value, err := jswire.DecodeGoja(goja.New(), raw)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return value.String()
}

// InvokeArgs encodes a map of arguments directly as a jswire object, without
// going through a JS runtime. A nil map encodes as a nil value.
func InvokeArgs(t testing.TB, args map[string]any) jswire.Value {
	t.Helper()
	if args == nil {
		return nil
	}
	raw, err := jswire.Encode(jswire.ObjectType(args))
	if err != nil {
		t.Fatalf("encode invoke args: %v", err)
	}
	return raw
}

// InvokeRunFunc matches invoke.Run. RunInvokeString takes the runner as a
// parameter because tooltest cannot import invoke: invoke's in-package tests
// import tooltest, so a direct import would create a test import cycle.
type InvokeRunFunc func(prepared toolset.PreparedToolset, toolName string, args jswire.Value) (jswire.Value, error)

// RunInvokeString runs a tool with InvokeArgs-encoded arguments and decodes
// the result to its string form. Pass invoke.Run as run.
func RunInvokeString(t testing.TB, run InvokeRunFunc, prepared toolset.PreparedToolset, toolName string, args map[string]any) (string, error) {
	t.Helper()
	raw, err := run(prepared, toolName, InvokeArgs(t, args))
	if err != nil {
		return "", err
	}
	value, err := jswire.DecodeGoja(goja.New(), raw)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
