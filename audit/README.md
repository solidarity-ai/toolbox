# audit

## Purpose

`audit` defines the shared event vocabulary emitted by execution-related packages.

Typical events include:
- execution events
- HTTP request/response events
- structured log events
- code session events

This package should primarily own shapes and schemas, not control flow.

## Who depends on this package

### `transport`
Emits HTTP-level audit events.

### `runtime/quickjs`
Emits runtime logs and execution events.

### `runtime/wasix`
Emits execution and subprocess-related events.

### `invoke`
Aggregates and normalizes execution output into a consistent result.

### `codemode`
Aggregates per-call audit into a session-level trace.

## What they use it for

- consistent event structures
- aggregation of execution traces
- returning introspection data to callers
- future persistence/export of audit trails

## What this package does not own

- transport policy
- runtime behavior
- storage of audit data
- orchestration
