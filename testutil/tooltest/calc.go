package tooltest

import (
	"testing"

	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func CalcAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	def, ok := tooldef.StubTSToolDef("calc.add")
	if !ok {
		t.Fatal("expected calc.add tool definition")
	}
	return def
}

func CalcSub(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	def, ok := tooldef.StubTSToolDef("calc.sub")
	if !ok {
		t.Fatal("expected calc.sub tool definition")
	}
	return def
}

func CalcAsyncAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	def, ok := tooldef.StubTSToolDef("calc.asyncAdd")
	if !ok {
		t.Fatal("expected calc.asyncAdd tool definition")
	}
	return def
}

func CalcToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	calcAdd := CalcAdd(t)
	calcSub := CalcSub(t)
	calcAsyncAdd := CalcAsyncAdd(t)

	return toolset.NewResolvedToolset([]toolset.Tool{
		{
			Name:        "calc.add",
			Description: "Add two numbers",
			TS:          &calcAdd,
		},
		{
			Name:        "calc.sub",
			Description: "Subtract two numbers",
			TS:          &calcSub,
		},
		{
			Name:        "calc.asyncAdd",
			Description: "Add two numbers asynchronously",
			TS:          &calcAsyncAdd,
		},
	})
}
