# Hill Chart — Stateless API View
**Updated**: 2026-03-23
**Session**: 01

## Scopes
  ✓ CEL Engine + Bindings + AgentView — Done (types, CEL compilation, AgentView with hidden param filtering, all tests passing)
  ✓ ValidateCall — Done (eval bindings at call time, inject hidden params, check expressions, all tests passing)
  ✓ Resource Param Inference — Done (InferResourceParams, two-tier binding flow, wired through Compile/Resolve, all tests passing)
  ▼ Codemode Refactor — Downhill (preludeForTools uses AgentView; typecheckSDKSource awaits TS metadata extension)
  ✓ MCP Service Wiring — Done (ToolsetService wraps ResolvedToolset, serves AgentView as discovery, executes via codemode)
  ▲ Extended TS Metadata Extraction — Uphill (not started; requires typescript-go fork changes for ParamTypes/ReturnType)

## Risk
Extended TS Metadata Extraction is the remaining risk — it requires modifying the typescript-go fork to extract per-param TS types and return types. This blocks the full codemode refactor of typecheckSDKSource.

## Next
Push TS metadata extraction uphill in next session (modify typescript-go fork).
