# api

## Purpose

`api` is the external protocol layer.

It translates MCP/HTTP requests into the internal architecture, but should remain thin. It is not the place where business logic, execution semantics, or runtime details should accumulate.

It owns:
- protocol request/response translation
- edge-level validation and normalization
- calling into `registry`, `toolset`, `invoke`, and `codemode`

## Who depends on this package

This is the outermost package area. External callers depend on it through MCP and HTTP, but internal packages generally should not depend on `api`.

## What it uses other packages for

- `registry` — load package/tool definitions
- `toolset` — assemble request-scoped tool availability
- `invoke` — execute one tool call
- `codemode` — execute one code session

## What this package does not own

- package fetching logic
- toolset semantics
- runtime selection
- JavaScript/WASM execution internals
- HTTP transport policy for tool execution

## Architectural rule

`api` must not call runtimes directly. It should stay a translation layer.
