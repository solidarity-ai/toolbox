# transport

## Purpose

`transport` owns outbound HTTP mediation.

It is the place where outbound requests are turned into controlled, observable network calls.

It owns:
- request/response mediation
- host and URL policy enforcement
- credential injection/materialization
- HTTP-level audit capture
- normalized transport errors

## Who depends on this package

### `runtime/quickjs`
Uses `transport` to satisfy host-imported network calls from TypeScript tools.

### `runtime/wasix`
Uses `transport` conceptually through execution-scoped proxy setup and audit collection for native WASM tools.

## What they use it for

- making outbound HTTP calls safely
- replacing credential placeholders with real secrets
- enforcing allowed-host rules
- capturing request/response-level audit data

## What this package does not own

- request-scoped toolset assembly
- CEL evaluation semantics
- runtime selection
- code session behavior
- package loading
