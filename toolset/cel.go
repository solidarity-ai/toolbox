package toolset

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

// compiledBinding holds a compiled CEL program for one binding expression.
type compiledBinding struct {
	Binding
	valueProgram cel.Program // compiled Value expression (nil if Value is empty)
	checkProgram cel.Program // compiled Check expression (nil if Check is empty)
}

// newCELEnv creates a CEL environment with "params" and "context" variables.
// Both are typed as map(string, dyn) to accept any tool param or context value.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("context", cel.MapType(cel.StringType, cel.DynType)),
	)
}

// compileBinding compiles a CEL expression string into a Program.
func compileBinding(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile CEL expression %q: %w", expr, issues.Err())
	}
	prg, err := env.Program(ast, cel.CostLimit(10000))
	if err != nil {
		return nil, fmt.Errorf("program CEL expression %q: %w", expr, err)
	}
	return prg, nil
}

// evalBinding evaluates a compiled value program with the given params and context.
func evalBinding(prg cel.Program, params map[string]any, ctx map[string]any) (any, error) {
	out, _, err := prg.Eval(map[string]any{
		"params":  params,
		"context": ctx,
	})
	if err != nil {
		return nil, fmt.Errorf("eval binding: %w", err)
	}
	return out.Value(), nil
}

// evalCheck evaluates a compiled check program. Returns true if the check passes.
func evalCheck(prg cel.Program, params map[string]any, ctx map[string]any) (bool, error) {
	out, _, err := prg.Eval(map[string]any{
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

// compileBindings compiles all bindings for a tool, returning compiledBinding entries.
func compileBindings(env *cel.Env, bindings map[string]Binding) (map[string]compiledBinding, error) {
	if len(bindings) == 0 {
		return nil, nil
	}

	compiled := make(map[string]compiledBinding, len(bindings))
	for paramName, binding := range bindings {
		cb := compiledBinding{Binding: binding}

		if binding.Value != "" {
			prg, err := compileBinding(env, binding.Value)
			if err != nil {
				return nil, fmt.Errorf("param %q value: %w", paramName, err)
			}
			cb.valueProgram = prg
		}

		if binding.Check != "" {
			prg, err := compileBinding(env, binding.Check)
			if err != nil {
				return nil, fmt.Errorf("param %q check: %w", paramName, err)
			}
			cb.checkProgram = prg
		}

		compiled[paramName] = cb
	}
	return compiled, nil
}
