package assembler

import "context"

// BuiltInFunc executes an app-owned tool that is exposed through the normal
// prepared-tool surface instead of being backed by a package runtime.
type BuiltInFunc func(ctx context.Context, args map[string]any) (string, error)
