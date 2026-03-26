# codemode

## Purpose

`codemode` owns code-session semantics.

It is distinct from plain tool execution. A code session lets an agent run code against a resolved toolset, with the toolset shaping the SDK surface the agent sees.

It owns:
- code session lifecycle
- SDK shape generation from a resolved toolset
- sequencing of repeated tool calls inside a session
- aggregation of session-level audit and results

## Who depends on this package

### `service`
Uses `codemode` to run agent-authored code sessions exposed over MCP or HTTP.

## What they use it for

- turning a resolved toolset into an SDK
- running agent code safely in a session
- reusing `invoke` for individual tool calls made during the session
- returning session-level results and audit traces

## What this package does not own

- direct runtime execution semantics for tools
- package loading
- request-scoped toolset assembly
- HTTP transport policy

## Architectural rule

`codemode` should delegate tool execution to `invoke`, not reimplement its own meaning of “run a tool”.
