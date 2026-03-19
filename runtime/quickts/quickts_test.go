package quickts_test

import (
	"testing"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestRunCalcAddStub(t *testing.T) {
	got, err := quickts.Run(tooltest.CalcAdd(t), map[string]any{
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
		"tools/calc.add.ts",
		`{"a":7,"b":4}`,
	)

	want := "import tool from \"./tools/calc.add.ts\";\n" +
		"export default await tool({\"a\":7,\"b\":4}, {});\n"
	if got != want {
		t.Fatalf("unexpected runner source:\nwant:\n%s\ngot:\n%s", want, got)
	}
}
