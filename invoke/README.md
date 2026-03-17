# invoke

## Purpose

`invoke` is the single orchestration seam for executing one tool call.

Given a resolved toolset, a selected tool, and call params, it:
- validates the call via `toolset`
- chooses the correct runtime
- executes the tool
- normalizes the result
- returns audit information in a consistent shape

This package exists to stop execution semantics from leaking into `api`, `codemode`, or the runtime packages.

## Who depends on this package

### `api`
Uses `invoke` to execute one tool call from MCP or HTTP.

### `codemode`
Uses `invoke` repeatedly during a code session whenever agent code calls a tool.

## What they use it for

- one canonical meaning of “run a tool”
- runtime selection
- normalized success/failure behavior
- shared execution semantics between direct tool calls and code sessions

## What this package does not own

- request-scoped assembly of a toolset
- sandbox internals
- protocol translation
- package fetching

## Architectural rule

`api` and `codemode` should not call runtimes directly. Tool execution should go through `invoke`.
