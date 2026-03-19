package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func CalcAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.add")
}

func CalcSub(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.sub")
}

func CalcAsyncAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.asyncAdd")
}

func CalcToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(calcFixtureDir()); err != nil {
		t.Fatalf("add calc package dir: %v", err)
	}
	return builder.Resolve()
}

func mustCalcTool(t testing.TB, name string) tooldef.TSToolDef {
	t.Helper()

	pkg, err := packaging.LoadSourcePackage(calcFixtureDir())
	if err != nil {
		t.Fatalf("load calc package: %v", err)
	}
	for _, tool := range pkg.ResolvedTools() {
		if tool.Name == name {
			if tool.TS == nil {
				t.Fatalf("tool %s has no TS definition", name)
			}
			return *tool.TS
		}
	}
	t.Fatalf("expected tool %s", name)
	return tooldef.TSToolDef{}
}

func calcFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "calc")
}
