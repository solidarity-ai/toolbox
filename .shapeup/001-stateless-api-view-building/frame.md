# Frame: Stateless API View

**Feature ID**: 001
**Created**: 2026-03-23
**Status**: Framing

---

## Problem

Today the only way to get an agent-visible tool surface from a resolved toolset is through `codemode`, which hardcodes SDK shape generation (namespace splitting, type declarations) as an internal detail of code execution. The MCP service layer has no equivalent — it can list raw tools from `toolset.Resolve()`, but there's no binding-aware, context-filtered view to serve. The toolset design doc describes a full binding model (CEL expressions, hidden params, check expressions, resource-level propagation, context injection), but `Resolve()` currently just passes through all tools from loaded packages with no filtering or transformation.

This means:
- MCP can't serve a proper tool list that reflects what an agent should actually see
- Codemode's SDK generation is coupled to its own execution internals rather than consuming a shared view
- There's no place to add bindings, hiding, or scoping without duplicating logic across consumers
- Sessions (future) have no clean foundation — there's no stateless "give me the API for this package + context" function to build on

The workaround today is that there is no workaround — MCP just dumps the raw tool list, and codemode does its own thing internally.

## Affected Segment

Internal architecture and both product surfaces (MCP server, codemode). Every downstream consumer of toolset — which is everything — is blocked on this being right.

## Business Value

This is foundational infrastructure for two things:
1. **Sessions** — a stateless API view is the building block for session-scoped execution. Without it, sessions have no clean input to work with.
2. **MCP product** — serving a binding-aware, context-filtered tool list is table stakes for a real MCP server.
3. **Codemode architecture** — moves SDK generation onto solid footing so codemode can evolve (discovery vs action modes, proxy-backed tool objects) without carrying inline shape logic.

Getting this right means the three core consumers (MCP, codemode, future sessions) all share one source of truth for "what does the agent see."

## Evidence

- `toolset.Resolve()` returns raw tools with no binding/context support (toolset/toolset.go:60-66)
- `codemode.preludeForTools()` and `codemode.typecheckSDKSource()` hardcode SDK shape generation (codemode/codemode.go:100-189)
- The toolset design doc (docs/pkg-toolset-design.md) describes the full binding model but none of it is implemented
- The codemode spec (docs/codemode-overview.md) explicitly states "toolset defines what is visible, codemode defines how that visible shape is presented as code" — but today codemode does both
- Service layer (service/) is a thin stub with no binding-aware tool listing

## Appetite

Big Batch: 6 weeks

Scope includes:
- Full CEL binding engine (value expressions, hidden params, check expressions)
- Resource-level binding propagation
- Context injection
- Shared `AgentView` output from toolset that both MCP and codemode consume
- Refactor codemode to consume AgentView instead of doing its own shape logic
- Wire MCP service to serve AgentView as a tool list

## Frame Statement

> "If we can shape this into something doable and execute within 6 weeks, it will give us the shared, stateless API view that MCP, codemode, and future sessions all need — replacing the current split where binding logic doesn't exist and SDK generation is trapped inside codemode."

---

## Status: Frame Go — approved 2026-03-23
