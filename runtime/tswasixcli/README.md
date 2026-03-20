# runtime/tswasixcli

## Purpose

`runtime/tswasixcli` is the Go-side adapter for the typescript+wasix-sandbox path.

It owns:
- shaping `exec(...)` requests from TypeScript tools
- calling the Rust `wasixcli` runtime
- returning normalized `stdout` / `stderr` / `exitCode` results to the TS tool layer

## Who depends on this package

### `invoke`
Uses `runtime/tswasixcli` when a TypeScript tool calls into the wasix sandbox path.

## What this package does not own

- TypeScript evaluation
- toolset resolution
- package fetching
- sandbox internals

## Current scope

The current implementation is intentionally narrow:
- `exec("<binary>", ...)` maps to `<package-root>/dist/<binary>.wasm`
- the Rust host binary is expected at `wasixcli-sandbox/target/debug/wasixcli-sandbox`
- general asset lookup and package-mounted executables are deferred
