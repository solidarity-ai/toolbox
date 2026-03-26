# Toolbox

Toolbox is a platform for giving agents better tools.

It provides:
- **Structured tool definitions** — tools carry parameters, metadata, constraints, and provenance
- **Request-scoped toolsets** — assemble a bounded, credentialed view of tools for one request or flow
- **Centralized execution semantics** — one place (`invoke`) that defines what "run a tool" means
- **Code mode** — agents can write code against a toolset-derived SDK instead of making raw tool calls
- **Strong introspection** — audit what was visible, what was called, and what happened

## Architecture

The system is organized around three core concepts:

| Concept | Package | Role |
|---|---|---|
| What exists | `tool` | Static package and tool definitions |
| What is available | `toolset` | Request-scoped assembly, bindings, credentials |
| How it runs | `invoke` | Single execution seam for one tool call |

See [`docs/architecture.md`](docs/architecture.md) for the full package diagram and data flows.

## Key packages

- [`tool`](tool/README.md) — static tool and package definitions
- [`toolset`](toolset/README.md) — request-scoped assembly and validation
- [`invoke`](invoke/README.md) — orchestration for one tool call
- [`codemode`](codemode/README.md) — code session semantics against a toolset
- [`runtime/quickts`](runtime/quickts/README.md) — TypeScript tool execution
- [`runtime/wasix`](runtime/wasix/README.md) — native WASM execution via the Rust host
- [`transport`](transport/README.md) — outbound HTTP mediation, credential injection, audit capture
- [`registry`](registry/README.md) — package acquisition and caching
- [`service`](service/README.md) — MCP/HTTP/CLI protocol layer
- [`audit`](audit/README.md) — shared execution event vocabulary
- [`secrets`](secrets/README.md) — secret store interface and local backend

## Module

```
github.com/solidarity-ai/toolbox
```

## Background

See [`docs/project.md`](docs/project.md) for the full project motivation and design principles.
