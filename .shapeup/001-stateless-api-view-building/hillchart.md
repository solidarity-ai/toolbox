# Hill Chart — Stateless API View
**Updated**: 2026-03-23
**Session**: 01

## Scopes
  ✓ Binding Types + CEL Engine — Done (types, compilation, evaluation all working)
  ✓ AgentView — Done (filtered tool surface with hidden params stripped)
  ✓ Enhanced Resolve — Done (Config input, CEL compilation, backward compat)
  ✓ ValidateCall — Done (invoke-time evaluation, check expressions, wired into invoke.Run)
  ✓ Resource Param Inference — Done (InferResourceParams, two-tier binding model, manifest overrides)
  ✓ Codemode Refactor — Done (preludeForTools uses AgentView)
  ✓ MCP Service Wiring — Done (Service.DiscoverTools returns AgentView)
  ~ Extended TS Metadata — Nice-to-have (requires typescript-go fork changes, deferred)

## Risk
No remaining must-have risks. Extended TS Metadata is deferred — codemode works via module imports.

## Next
All must-haves complete. Ready for PR and review.
