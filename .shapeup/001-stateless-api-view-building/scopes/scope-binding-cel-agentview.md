# Scope: Binding + CEL + AgentView

## Hill Position
✓ Done

## Must-Haves
- [x] Binding, BoundTool, Config types in toolset/binding.go
- [x] Internal CEL engine in toolset/cel.go (compile + eval)
- [x] AgentView, AgentTool, ParamType types in toolset/agentview.go
- [x] Enhanced Resolve(cfg Config) with CEL compilation
- [x] Hidden param removal from AgentView schema
- [x] Backward compat: Config{} gives same result as before
- [x] Update all callers (tooltest, mcpserver tests)

## Notes
- cel-go v0.27.0 added. Required genproto version bump in devtools/go.mod.
- ReadOnly and Idempotent flags propagated through AgentView from PackageTool metadata.
