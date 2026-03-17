# runtime/quickjs

## Purpose

`runtime/quickjs` executes TypeScript/JavaScript tools.

It owns:
- JavaScript engine/session lifecycle
- host import wiring
- loading/evaluating tool code
- mapping host imports to Go capabilities
- runtime-local error normalization

## Who depends on this package

### `invoke`
Uses `runtime/quickjs` when the selected tool is a TS/JS tool.

### `codemode`
May depend on the same engine primitives conceptually, but should still execute tools through `invoke` rather than reproducing tool-call semantics itself.

## What they use it for

- evaluating TS/JS tool logic
- exposing host imports like `fetch`, `exec`, `mcp`, and `log`
- capturing runtime logs and execution results

## What this package does not own

- toolset resolution
- binding semantics
- runtime selection
- package loading
- protocol handling
