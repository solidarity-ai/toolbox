# runtime/tswasmer

## Purpose

`runtime/tswasmer` is the Go-side adapter for the typescript+wasmer-sandbox path.

It owns:
- shaping `exec(...)` requests from TypeScript tools
- calling the Rust `wasmersandbox` runtime
- decoding the normalized sandbox result

## Who depends on this package

### `invoke`
Uses `runtime/tswasmer` when a TypeScript tool calls into the wasmer sandbox path.

## What this package does not own

- TypeScript evaluation
- toolset resolution
- package fetching
- sandbox internals
