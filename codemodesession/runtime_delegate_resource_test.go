package codemodesession

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestCallableResourceIntrinsicAliases(t *testing.T) {
	rt := goja.New()
	factory := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		return call.Arguments[0]
	}
	target := rt.ToValue(factory).ToObject(rt)
	originalName := target.Get("name").String()
	originalLength := target.Get("length").ToInteger()
	node := &tooldef.ResourceAPINode{Methods: []tooldef.ResourceAPIMethod{
		{Name: "name"},
		{Name: "length"},
		{Name: "prototype"},
		{Name: "call"},
		{Name: "apply"},
		{Name: "bind"},
		{Name: "caller"},
		{Name: "arguments"},
		{Name: "constructor"},
	}}
	if err := installCallableIntrinsicAliases(rt, target, node); err != nil {
		t.Fatalf("installCallableIntrinsicAliases() error: %v", err)
	}

	for _, name := range []string{"name", "length", "prototype", "call", "apply", "bind", "caller", "arguments", "constructor"} {
		if _, ok := goja.AssertFunction(target.Get("_" + name)); !ok {
			t.Fatalf("_%s is not callable", name)
		}
		if err := defineRuntimeAPIMember(target, name, rt.ToValue(func(goja.FunctionCall) goja.Value {
			return rt.ToValue("tool:" + name)
		})); err != nil {
			t.Fatalf("defineRuntimeAPIMember(%q) error: %v", name, err)
		}
	}

	if got := callNoArgs(t, target, "_name").String(); got != originalName {
		t.Fatalf("_name() = %q, want %q", got, originalName)
	}
	if got := callNoArgs(t, target, "_length").ToInteger(); got != originalLength {
		t.Fatalf("_length() = %d, want %d", got, originalLength)
	}
	if got := callNoArgs(t, target, "_prototype"); !goja.IsUndefined(got) {
		t.Fatalf("_prototype() = %v, want undefined", got)
	}
	callAlias, ok := goja.AssertFunction(target.Get("_call"))
	if !ok {
		t.Fatal("_call is not callable")
	}
	got, err := callAlias(target, goja.Undefined(), rt.ToValue("selected"))
	if err != nil {
		t.Fatalf("_call() error: %v", err)
	}
	if got.String() != "selected" {
		t.Fatalf("_call(undefined, selected) = %v, want selected", got)
	}
	applyAlias, _ := goja.AssertFunction(target.Get("_apply"))
	got, err = applyAlias(target, goja.Undefined(), rt.ToValue([]string{"applied"}))
	if err != nil || got.String() != "applied" {
		t.Fatalf("_apply(undefined, [applied]) = %v, %v; want applied", got, err)
	}
	bindAlias, _ := goja.AssertFunction(target.Get("_bind"))
	bound, err := bindAlias(target, goja.Undefined(), rt.ToValue("bound"))
	if err != nil {
		t.Fatalf("_bind() error: %v", err)
	}
	boundCall, ok := goja.AssertFunction(bound)
	if !ok {
		t.Fatal("_bind() did not return a function")
	}
	got, err = boundCall(goja.Undefined())
	if err != nil || got.String() != "bound" {
		t.Fatalf("bound selector() = %v, %v; want bound", got, err)
	}
	constructorAlias, _ := goja.AssertFunction(target.Get("_constructor"))
	constructed, err := constructorAlias(target, rt.ToValue("return 7"))
	if err != nil {
		t.Fatalf("_constructor() error: %v", err)
	}
	constructedCall, ok := goja.AssertFunction(constructed)
	if !ok {
		t.Fatal("_constructor() did not return a function")
	}
	got, err = constructedCall(goja.Undefined())
	if err != nil || got.ToInteger() != 7 {
		t.Fatalf("constructed function() = %v, %v; want 7", got, err)
	}
	selector, ok := goja.AssertFunction(target)
	if !ok {
		t.Fatal("selector is no longer callable")
	}
	got, err = selector(goja.Undefined(), rt.ToValue("direct"))
	if err != nil || got.String() != "direct" {
		t.Fatalf("selector(direct) = %v, %v", got, err)
	}
	if !strings.HasPrefix(callNoArgs(t, target, "name").String(), "tool:") {
		t.Fatal("generated name method was not installed")
	}
}

func TestCallableResourceChildIntrinsicAlias(t *testing.T) {
	rt := goja.New()
	target := rt.ToValue(func(goja.FunctionCall) goja.Value { return goja.Undefined() }).ToObject(rt)
	originalName := target.Get("name").String()
	node := &tooldef.ResourceAPINode{Children: []tooldef.ResourceAPIChild{{Name: "name"}}}
	if err := installCallableIntrinsicAliases(rt, target, node); err != nil {
		t.Fatalf("installCallableIntrinsicAliases() error: %v", err)
	}
	child := rt.NewObject()
	if err := defineRuntimeAPIMember(target, "name", child); err != nil {
		t.Fatalf("define child property: %v", err)
	}
	if got := callNoArgs(t, target, "_name").String(); got != originalName {
		t.Fatalf("_name() = %q, want displaced function name %q", got, originalName)
	}
	if got := target.Get("name"); got != child {
		t.Fatalf("name child = %v, want installed child object", got)
	}
}

func callNoArgs(t testing.TB, target *goja.Object, name string) goja.Value {
	t.Helper()
	callable, ok := goja.AssertFunction(target.Get(name))
	if !ok {
		t.Fatalf("%s is not callable", name)
	}
	value, err := callable(target)
	if err != nil {
		t.Fatalf("%s() error: %v", name, err)
	}
	return value
}
