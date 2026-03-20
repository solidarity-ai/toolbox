# runtime/tswasmer

## Purpose

`runtime/tswasmer` is the Go-side adapter for the typescript+wasmer-sandbox path.

It owns:
- shaping `exec(...)` requests from TypeScript tools
- calling the Rust `wasmersandbox` runtime
- returning normalized `stdout` / `stderr` / `exitCode` results to the TS tool layer

## Who depends on this package

### `invoke`
Uses `runtime/tswasmer` when a TypeScript tool calls into the wasmer sandbox path.

## What this package does not own

- TypeScript evaluation
- toolset resolution
- package fetching
- sandbox internals

## Current scope

The current implementation is intentionally narrow:
- `exec("<binary>", ...)` maps to `<package-root>/dist/<binary>.wasm`
- the Rust host binary is expected at `wasmersandbox/target/debug/wasmersandbox`
- general asset lookup and package-mounted executables are deferred
