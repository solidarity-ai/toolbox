# runtime/tswasmcli

## Purpose

`runtime/tswasmcli` is the Go-side adapter for WASM sandbox runtimes (wasix-cli and wasip2-cli).

It owns:
- shaping `exec(...)` requests from TypeScript tools
- calling the Rust `wasmcli-sandbox` binary with the appropriate `--runtime` flag
- returning normalized `stdout` / `stderr` / `exitCode` results to the TS tool layer

## Who depends on this package

### `invoke`
Uses `runtime/tswasmcli` when a TypeScript tool calls into the WASM sandbox path.

## What this package does not own

- TypeScript evaluation
- toolset resolution
- package fetching
- sandbox internals

## Current scope

The current implementation is intentionally narrow:
- `exec("<binary>", ...)` maps to `<package-root>/dist/<binary>.wasm`
- the Rust host binary is expected at `wasmcli-sandbox/target/debug/wasmcli-sandbox`
- general asset lookup and package-mounted executables are deferred
