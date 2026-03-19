# Toolbox — Project Overview

## What this project is

Toolbox is a platform for giving agents **better tools**.

It is being built around a few core ideas:

1. **Tools should be more than raw MCP functions.**
   A tool should carry structure: parameters, metadata, constraints, provenance, and execution behavior.

2. **A harness should be able to construct a request-scoped view of tools.**
   An agent should not just be handed a giant bag of capabilities. Instead, a caller should be able to assemble a **toolset** for one request or flow, with bindings, context, and credentials attached.

3. **Agents should be able to use tools in two ways:**
   - direct tool calls
   - code mode, where the agent writes code against an SDK derived from the current toolset

4. **Execution should work both locally and remotely.**
   Toolbox is intended to support:
   - local library-style use
   - a hosted Toolbox product
   - agent-driven CLI use
   - MCP adapters
   - custom harnesses

5. **Introspection matters.**
   The system should make it easy to understand what happened: what tools were visible, what was called, what requests were made, what failed, and why.

---

## Why we are building it

Current tool systems for agents are too weak.

Problems with the current ecosystem:
- tools are often just loosely described functions with little policy or structure
- permissioning is weak or ad hoc
- scope is hard to express cleanly
- tool provenance is often unclear
- different harnesses rebuild the same concepts repeatedly
- agent code and tool execution are often hard to introspect
- the distinction between read-safe and destructive operations is often lost

Toolbox is an attempt to create a stronger foundation for agent tooling.

The project is motivated by the belief that agents need:
- better capability modeling
- safer scoped execution
- better composability
- better auditability
- a path from local experimentation to a hosted product

---

## Current architectural direction

The architecture is being designed **outside in**.

We are not starting from REST endpoints or transport details. We are starting from the core concepts that callers need, and then shaping packages around those concepts.

The most important concepts today are:

### `tool`
Static package and tool definitions.

This is the inert definition world:
- package identity
- tool identity
- params
- metadata
- resource-path naming

### `toolset`
The request-scoped assembled view of what tools are available and how they are bound.

A toolset contains:
- selected tools
- bindings
- context
- credentials

A toolset is what a harness prepares for one request or flow.

### `invoke`
The one place where a single tool call is executed correctly.

This package is meant to prevent architectural drift. Other packages should not invent their own meaning of “run a tool”.

### `codemode`
A distinct execution mode where agent-authored code runs against a resolved toolset-derived SDK.

CodeMode is not just “tool invocation with extra steps.” It is its own product concept, but it should still delegate actual tool execution to `invoke`.

### `runtime/quickjs`
Execution backend for TypeScript/JavaScript tools.

### `runtime/wasix`
Execution backend for native WASM tools via a Rust host.

### `transport`
Outbound HTTP mediation, credentials, and audit capture.

### `registry`
Package acquisition and caching.

---

## Design principles

### 1. Keep the public mental model small
We are trying to avoid an IAM-like explosion of concepts.

The current core mental model is roughly:
- Tool
- Toolset
- Binding
- Context
- Invoke

Everything else should ideally either be:
- an implementation detail
- or a convenience layer on top

### 2. Keep CEL inside toolset
CEL is currently used as the expression language for toolset bindings and checks.

It should be treated as an implementation detail of request-scoped toolset assembly, not as a system-wide public abstraction.

### 3. Keep runtimes dumb
Runtimes should receive prepared execution requests.

They should not know:
- toolset semantics
- binding semantics
- request-scoped policy decisions
- API-level behavior

### 4. Keep execution semantics centralized
If something means “run a tool”, that meaning should live in `invoke`.

### 5. Support custom harnesses directly
A custom harness should not be forced through a heavy service/session abstraction.

The core packages should be usable directly:
- assemble toolset
- invoke tools
- run codemode

A higher-level service layer can exist later for local/remote convenience.

### 6. Design for both local and hosted use
Toolbox is not just a local library. It is also intended to support a hosted product.

That means we care about:
- locality boundaries
- remote execution
- provenance and trust
- reproducibility
- inspectability

---

## What CodeMode means here

CodeMode is important enough to call out separately.

The current direction is:
- CodeMode consumes a **resolved toolset snapshot**
- It generates an SDK shape from the agent-visible toolset
- Agent-authored code runs against that SDK
- Every actual tool call made from CodeMode delegates to `invoke`
- CodeMode aggregates a session-level trace

This keeps responsibilities clean:
- `toolset` decides what exists and what is visible
- `invoke` decides how one tool call executes
- `codemode` decides how agent code interacts with tools over time

---

## Local vs hosted

This project is expected to eventually support:

### Local use
- direct Go library use
- local development harnesses
- local CLI-driven agent workflows

### Hosted use
- a remote Toolbox product
- potentially remote package/tool execution
- remote audit/introspection
- clients that talk to a managed Toolbox service

The current design goal is to avoid making the hosted model distort the core package model too early.

Core packages should stand on their own.
A service/session layer can be added above them.

---

## Native WASM direction

For native tools, the current direction is:
- use a thin Rust WASIX host
- keep Go as the place where the intelligence lives
- treat the Rust host as a stable execution appliance
- support modes like:
  - proxy-mediated networking
  - open networking
  - no networking

For TS/JS tools, the likely direction is to use QuickJS from Go.

This gives two runtime paths:
- a Go-native JS execution path
- a Rust-hosted native WASM execution path

---

## High-risk areas

One of the highest-risk technical areas currently identified is a shared virtual filesystem across runtimes.

Why this is high risk:
- TypeScript code may need a Node-style `fs` view
- native WASM tools may need a real VFS inside the Rust host
- CLI-style tools may write files that later TypeScript code needs to read back and include in final results
- CodeMode and direct tool execution both benefit from one consistent scratch/filesystem model during a run

This is not just a CodeMode concern. It is a cross-runtime execution concern that affects:
- runtime interoperability
- result handling
- intermediate artifacts
- caching
- isolation boundaries

The current direction is to treat this as a shared runtime concern rather than inventing separate filesystem models per runtime.

---

## Current open questions

Some areas are intentionally not settled yet:
- the exact CodeMode SDK shape
- how service/session abstractions should sit above toolsets
- the exact trust/provenance model for tool packages
- how much of the system should be local-first vs remote-first
- what the minimum useful hosted surface is

This is expected. The project is still in design/spike mode.

---

## If you are a new agent working on this repo

When in doubt:

1. **Preserve the package boundaries.**
2. **Do not make runtimes smarter than they need to be.**
3. **Do not bypass `invoke` for tool execution.**
4. **Do not turn CEL into a system-wide concept.**
5. **Prefer request-scoped resolved toolsets as the core execution input.**
6. **Treat CodeMode as distinct from plain tool invocation.**

If you need architectural context, read:
- `docs/architecture.md`
- `tool/README.md`
- `toolset/README.md`
- `invoke/README.md`
- `codemode/README.md`

---

## Short version

Toolbox is a platform for building, constraining, composing, and running agent tools better than today’s raw tool protocols allow.

It is trying to provide:
- better tool definitions
- request-scoped toolsets
- centralized execution semantics
- code mode for agents
- strong introspection
- local and hosted execution paths

The project is currently focused on getting the architecture and boundaries right before filling in implementation details.
