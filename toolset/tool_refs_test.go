package toolset

import (
	"slices"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestPreparedToolRefs(t *testing.T) {
	prepared := NewPreparedToolset([]assembler.LoadedTool{
		{
			Name: "calc.add",
			PackageMeta: &tooldef.Package{
				Module: "example.com/math",
			},
			PackageVersion: "v1.2.3",
		},
		{
			Name: "calc.sub",
			PackageMeta: &tooldef.Package{
				Module: "example.com/math",
			},
		},
		{
			Name: "builtin.echo",
		},
	})

	got := PreparedToolRefs(prepared)
	want := []string{
		"builtin.echo",
		"example.com/math/calc.sub",
		"example.com/math@v1.2.3/calc.add",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("PreparedToolRefs() = %#v, want %#v", got, want)
	}
}
