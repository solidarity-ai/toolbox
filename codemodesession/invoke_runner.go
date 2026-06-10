package codemodesession

import (
	"context"

	"github.com/mackross/repljs/jswire"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

func runPreparedTool(ctx context.Context, executor *invoke.Executor, prepared toolset.PreparedToolset, toolName string, args jswire.Value) (jswire.Value, error) {
	if executor != nil {
		return executor.RunContext(ctx, prepared, toolName, args)
	}
	return invoke.RunContext(ctx, prepared, toolName, args)
}
