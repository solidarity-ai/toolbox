package toolset

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

// newCELEnv creates a CEL environment with params and context variables
// available for binding expressions.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("context", cel.MapType(cel.StringType, cel.DynType)),
	)
}

// compileBinding compiles a CEL expression string into a program.
// Used at resolve time to validate expressions early.
func compileBinding(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile CEL expression %q: %w", expr, issues.Err())
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("program CEL expression %q: %w", expr, err)
	}
	return prog, nil
}

// evalBinding evaluates a compiled CEL binding program with the given params and context.
func evalBinding(prog cel.Program, params map[string]any, ctx map[string]any) (any, error) {
	out, _, err := prog.Eval(map[string]any{
		"params":  params,
		"context": ctx,
	})
	if err != nil {
		return nil, fmt.Errorf("eval binding: %w", err)
	}
	return out.Value(), nil
}

// evalCheck evaluates a compiled CEL check program. Returns true if the check passes.
func evalCheck(prog cel.Program, params map[string]any, ctx map[string]any) (bool, error) {
	out, _, err := prog.Eval(map[string]any{
		"params":  params,
		"context": ctx,
	})
	if err != nil {
		return false, fmt.Errorf("eval check: %w", err)
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("check expression must return bool, got %T", out.Value())
	}
	return result, nil
}
