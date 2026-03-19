# runtime

## Purpose

`runtime` contains execution backends.

A runtime receives a prepared execution request and runs it inside a sandbox or process boundary. Runtimes do not decide whether a tool call is allowed; they only execute what has already been prepared and validated.

Current planned runtimes:
- `runtime/quickjs` — TypeScript/JavaScript tool execution
- `runtime/tswasmer` — typescript+wasmer-sandbox execution via the Rust host

## Who depends on this package area

### `invoke`
Uses a runtime to execute a prepared tool call.

## What runtimes should know

- the executable artifact to run
- fully resolved params
- execution limits
- required capabilities (transport, assets, logging)

## What runtimes must not know

- how bindings are authored
- how context semantics work at the toolset level
- how tool visibility is decided
- MCP/HTTP protocol details

## Architectural rule

Runtimes receive prepared execution requests only. They should remain dumb about request-scoped policy and toolset semantics.
