# tool

## Purpose

`tool` defines the static package and tool vocabulary for the system.

It owns inert definition data:
- package identity
- manifest shape
- tool definitions
- parameter schemas
- tool metadata
- resource-path parsing/inference

This package is intentionally boring. It should be one of the most stable packages in the repo.

## Who depends on this package

### `toolset`
Uses `tool` definitions as the input to request-scoped assembly.

### `registry`
Returns loaded packages in terms of `tool` types.

### `runtime/quickjs`
Uses tool definitions to understand what is being executed and what host capabilities are needed.

### `runtime/wasix`
Uses tool/package metadata associated with native WASM execution.

### `invoke`
Uses tool identity and definition information when selecting and normalizing execution.

## What this package does not own

- request-scoped bindings
- context
- credentials
- CEL
- tool execution
- HTTP transport
- API protocol translation
