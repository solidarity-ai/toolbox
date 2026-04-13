package main

import (
	"reflect"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestParseEffectFilter(t *testing.T) {
	t.Run("accepts supported effects", func(t *testing.T) {
		got, err := parseEffectFilter("readonly, reversible, readOnly, irreversible")
		if err != nil {
			t.Fatalf("parseEffectFilter() error: %v", err)
		}

		want := map[tooldef.Effect]bool{
			tooldef.EffectReadOnly:     true,
			tooldef.EffectReversible:   true,
			tooldef.EffectIrreversible: true,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parseEffectFilter() = %#v, want %#v", got, want)
		}
	})

	t.Run("rejects unknown effects", func(t *testing.T) {
		_, err := parseEffectFilter("readonly, destructive")
		if err == nil {
			t.Fatal("parseEffectFilter() error = nil, want non-nil")
		}
		if got := err.Error(); got != `effects="destructive" is not supported; use readonly,reversible,irreversible` {
			t.Fatalf("parseEffectFilter() error = %q, want exact guidance", got)
		}
	})
}

func TestFilterPreparedToolsetByEffects(t *testing.T) {
	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{
		{Name: "pkg.read", Effect: tooldef.EffectReadOnly},
		{Name: "pkg.undo", Effect: tooldef.EffectReversible},
		{Name: "pkg.send", Effect: tooldef.EffectIrreversible},
		{Name: "pkg.unknown"},
	})

	got, err := filterPreparedToolsetByEffects(prepared, "readonly,irreversible")
	if err != nil {
		t.Fatalf("filterPreparedToolsetByEffects() error: %v", err)
	}

	want := []string{"pkg.read", "pkg.send"}
	if gotNames := preparedToolNames(got); !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("filtered tool names = %#v, want %#v", gotNames, want)
	}
}

func preparedToolNames(prepared toolset.PreparedToolset) []string {
	tools := prepared.Tools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}
