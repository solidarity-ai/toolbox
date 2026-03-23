# Hill Chart — Stateless API View
**Updated**: 2026-03-23
**Session**: 01

## Scopes
  ▲ Binding + CEL + AgentView — Uphill (investigating, starting first piece)
  ▲ ValidateCall — Uphill (not started, depends on binding engine)
  ▲ Resource Param Inference — Uphill (not started)
  ▲ Extended TS Metadata — Uphill (not started, in typescript-go fork)
  ▲ Codemode Refactor — Uphill (not started, depends on AgentView)
  ▲ MCP Service Wiring — Uphill (not started, depends on AgentView)

## Risk
CEL engine integration is the riskiest unknown — new dependency, compilation/evaluation model untested in this codebase.

## Next
Push "Binding + CEL + AgentView" uphill: define types, integrate cel-go, write tests, prove hidden params work.
