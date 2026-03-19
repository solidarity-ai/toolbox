package codemode_test

import (
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/codemode"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestRunChainsToolCalls(t *testing.T) {
	result, err := codemode.Run(tooltest.CalcToolset(t), `
const x: number = Number(tools.calc.add({ a: 5, b: 5 }));
export default tools.calc.sub({ a: x, b: 1 });
`)
	if err != nil {
		t.Fatalf("run codemode: %v", err)
	}

	if result != "9" {
		t.Fatalf("expected 9, got %q", result)
	}
}

func TestRunAsyncToolCall(t *testing.T) {
	result, err := codemode.Run(tooltest.CalcToolset(t), `
export default tools.calc.asyncAdd({ a: 7, b: 4 });
`)
	if err != nil {
		t.Fatalf("run codemode: %v", err)
	}

	if result != "11" {
		t.Fatalf("expected 11, got %q", result)
	}
}

func TestRunTypeMismatchBetweenToolsFails(t *testing.T) {
	_, err := codemode.Run(tooltest.CalcToolset(t), `
const x = tools.calc.add({ a: 5, b: 5 });
export default tools.calc.sub({ a: x, b: 1 });
`)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
	if !strings.Contains(err.Error(), "typescript check failed") {
		t.Fatalf("expected typescript check failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "string") || !strings.Contains(err.Error(), "number") {
		t.Fatalf("expected string/number mismatch, got %v", err)
	}
}

func TestRunRejectsDirectToolModuleImport(t *testing.T) {
	_, err := codemode.Run(tooltest.CalcToolset(t), `
import { execute } from "./github.com/solidarity-ai/calc-tools/tools/calc.add.ts";
export default execute({ a: 5, b: 5 }, {});
`)
	if err == nil {
		t.Fatal("expected direct tool module import to be rejected")
	}
}

func TestRunRejectsDirectSharedPackageImport(t *testing.T) {
	_, err := codemode.Run(tooltest.CalcToolset(t), `
import { internalValue } from "./github.com/solidarity-ai/calc-tools/lib/internal.ts";
export default internalValue();
`)
	if err == nil {
		t.Fatal("expected direct shared package import to be rejected")
	}
}

func TestRunRejectsDirectInvokeHookAccess(t *testing.T) {
	_, err := codemode.Run(tooltest.CalcToolset(t), `
const invokeTool = (globalThis as any).__invokeTool;
const x = Number(tools.calc.add({ a: 5, b: 5 }));
export default invokeTool("calc.sub", { a: x, b: 1 });
`)
	if err == nil {
		t.Fatal("expected direct invoke hook access to be rejected")
	}
}
