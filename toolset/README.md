# toolset

## Purpose

`toolset` owns request-scoped resolution over already-loaded tools: bindings,
agent-visible schemas, validation, and resolved call parameters.

It owns:
- bindings
- context
- request-scoped resolution over preloaded tools
- agent-visible tool view
- call validation and parameter resolution
- account-selection parameter injection

This is the product brain of the system.

## Who depends on this package

### `invoke`
Uses `toolset` as the source of truth for whether a tool call is valid and what
the fully resolved params should be.

### `mcpserver`
Uses `toolset` to expose the agent-visible tool surface and validate incoming
tool calls.

### `codemode`
Uses `toolset` to derive the SDK shape that an agent sees in a code session.

### `toolsetfile`
Calls `toolset.ResolveTools` after package assembly completes.

## What they use it for

- resolving bindings against context
- compiling and evaluating CEL-based values/checks
- hiding params from the agent-visible schema
- generating an agent-visible tool listing for one request
- validating a tool call before execution
- injecting account-selection params for multi-account credentials

## What this package does not own

- package fetching/loading
- declaration assembly
- registry/cache provenance
- runtime selection
- sandbox execution
- HTTP transport details
- MCP or HTTP protocol handling

## Important note

CEL is an implementation detail of `toolset`, not a separate platform-level concept. Other packages should not reason about request-scoped policy semantics directly.
