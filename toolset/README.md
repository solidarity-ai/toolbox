# toolset

## Purpose

`toolset` assembles the request-scoped view of what tools are available, how they are bound, and what the agent is allowed to see and pass.

It owns:
- toolset composition
- bound tools
- bindings
- context
- credential attachment
- request-scoped resolution
- agent-visible tool view
- call validation and parameter resolution

This is the product brain of the system.

## Who depends on this package

### `api`
Uses `toolset` to turn harness input into a resolved request-scoped execution context.

### `invoke`
Uses `toolset` as the source of truth for whether a tool call is valid and what the fully resolved params should be.

### `codemode`
Uses `toolset` to derive the SDK shape that an agent sees in a code session.

## What they use it for

- resolving bindings against context
- compiling and evaluating CEL-based values/checks
- hiding params from the agent-visible schema
- generating a narrowed tool listing for one request
- validating a tool call before execution

## What this package does not own

- package fetching/loading
- runtime selection
- sandbox execution
- HTTP transport details
- MCP or HTTP protocol handling

## Important note

CEL is an implementation detail of `toolset`, not a separate platform-level concept. Other packages should not reason about request-scoped policy semantics directly.
