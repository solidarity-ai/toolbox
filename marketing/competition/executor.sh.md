# `executor` / executor.sh: Positioning Through Guarantees

This note is not about dunking on `executor`.

`executor` is useful because it already proves there is demand for:
- local-first agent execution
- typed tool access from TypeScript
- connected source catalogs
- pause/resume for auth and approval flows

The stronger Toolbox positioning is not "we also run code against tools."

The stronger positioning is:

> Toolbox is designed to make stronger guarantees about what tools exist, what they are allowed to do, how they are scoped for one request, and how execution is interpreted across direct calls, code mode, local use, and hosted use.

## Core positioning idea

`executor` appears optimized around a local control plane with a durable workspace tool catalog.

Toolbox is being designed around:
- versioned tool/package definitions
- request-scoped resolved toolsets
- explicit tool metadata and constraints
- one centralized invocation seam
- immutable code-mode snapshots

That gives us a better basis for guarantees.

## Guarantees Toolbox can aim to make

### 1. Request-scoped capability guarantee

For one request or flow, the harness can assemble a minimal toolset and hand the agent only that resolved view.

That means Toolbox can say:
- the agent did not receive the whole workspace catalog
- the visible SDK surface for this run was intentionally constructed
- hidden params, credentials, and bindings were attached by the harness, not improvised inside runtime code

This is stronger than a generic "the workspace has these tools" story.

### 2. Stable snapshot guarantee for code mode

Because CodeMode is designed around a resolved snapshot, Toolbox can aim to guarantee:
- the SDK shape for one action run is pinned
- hidden bindings and checks do not silently drift during the run
- audit can point back to the exact snapshot used

This is especially important for multi-step agent code.

The positioning line is:

> The agent does not act against "whatever the workspace looks like right now." It acts against a pinned snapshot.

### 3. Explicit safety semantics guarantee

Tool metadata is not just documentation. It is part of the execution model.

Toolbox can aim to make explicit guarantees around:
- `readOnly`
- `destructive`
- `idempotent`
- credential requirements
- allowed hosts
- resource limits

That gives the harness and product layer something concrete to reason about before execution.

This is stronger than "the tool probably behaves this way."

### 4. Bound-scope guarantee

Because bindings are first-class in `toolset`, Toolbox can express:
- which params are agent-provided
- which params are pre-bound
- which params are hidden
- what checks must pass before execution

That gives us a strong positioning line:

> Scope is not a prompt convention. Scope is part of the assembled toolset.

This matters for customer scoping, environment scoping, allowed channels, tenant IDs, and similar boundaries.

### 5. One execution-semantics guarantee

Toolbox is explicitly designed so "run a tool" means one thing, in one place: `invoke`.

That lets us position around:
- consistent behavior across direct calls and CodeMode
- one place for retries, normalization, audit shape, and runtime selection
- less architectural drift between product surfaces

The practical claim is:

> If execution semantics change, they change once.

### 6. Runtime-independence guarantee

Toolbox keeps runtimes dumb.

That means we can position around:
- the same tool definition model working across QuickJS and WASIX
- runtime choice not changing toolset semantics
- product policy staying above the runtime boundary

This becomes more important over time as native WASM and hosted execution matter more.

### 7. Provenance and pinning guarantee

Because packages are versioned and intended to be pinned, Toolbox can later offer stronger guarantees around:
- exactly what source/version produced a tool
- what manifest and metadata were in effect
- what provenance and trust policy applied

That is a meaningful positioning lever once teams care about compliance, audit, and reproducibility.

### 8. Local/hosted portability guarantee

Toolbox is being designed so the core packages are usable directly and are not distorted around one local product shape.

That means we can aim to say:
- the same core model works in library use, CLI use, custom harnesses, MCP adapters, and a hosted product
- callers are not locked into one daemon-oriented product architecture

## Where `executor` is still strong

We should be honest about the things `executor` already demonstrates well:
- a coherent local-first product
- connected source ingestion
- TypeScript-friendly developer experience
- built-in discovery tools
- human-in-the-loop pause/resume

If we compare, we should compare on guarantees and architecture, not pretend they have no value.

## Positioning lines to reuse later

- Toolbox gives agents a request-scoped toolbox, not a giant workspace bag of capabilities.
- Toolbox can tell you not just what tool was called, but what snapshot, bindings, and policy context made that call possible.
- Toolbox treats read-only, destructive, idempotent, host scope, and resource limits as first-class execution metadata.
- Toolbox separates what exists, what is visible for this request, and what it means to execute a call.
- Toolbox is designed so local and hosted execution share the same core semantics.

## Claims to be careful with

We should avoid overclaiming until implemented.

In particular, these are current design advantages, not finished product guarantees:
- immutable action snapshots end to end
- provenance and trust enforcement
- full hosted/local parity
- final CodeMode TypeScript pipeline and compile-time guarantees

The right posture is:

> We are designing for stronger guarantees than a local catalog runtime can usually offer, because those guarantees are represented directly in the core model.
