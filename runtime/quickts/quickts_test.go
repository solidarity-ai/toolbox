package quickts_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestRunCalcAddStub(t *testing.T) {
	def, ok := tooldef.StubTSToolDef("calc.add")
	if !ok {
		t.Fatalf("expected calc.add stub tool def")
	}

	got, err := quickts.Run(def, map[string]any{
		"a": 7,
		"b": 4,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "11" {
		t.Fatalf("expected 11, got %q", got)
	}
}

func TestRunnerSourceEmbedsJSONArgs(t *testing.T) {
	got := quickts.RunnerSourceForTest(
		"github.com/solidarity-ai/calc-tools/tools/calc.add.ts",
		`{"a":7,"b":4}`,
	)

	want := "import { execute as add } from \"./github.com/solidarity-ai/calc-tools/tools/calc.add.ts\";\n" +
		"export default add({\"a\":7,\"b\":4}, {});\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}
