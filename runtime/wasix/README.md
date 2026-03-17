# runtime/wasix

## Purpose

`runtime/wasix` executes native WASM tools via the Rust WASIX host.

It owns:
- Go-side execution request preparation for the Rust host
- subprocess lifecycle
- stdin/stdout protocol with the Rust host
- execution-scoped temp directories, env, and mounts
- proxy/open-mode setup and result decoding

## Who depends on this package

### `invoke`
Uses `runtime/wasix` when the selected tool is a native WASM tool.

## What they use it for

- launching the Rust execution host
- running native WASM tools with filesystem, env, and network config
- getting normalized subprocess execution results back into Go

## What this package does not own

- request-scoped toolset semantics
- CEL
- package loading
- MCP/HTTP protocol handling
- deciding whether a tool call should happen

## Important boundary

This package is the Go-side adapter for the Rust host. It should stay narrow and focused on execution plumbing.
