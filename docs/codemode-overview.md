# `codemode` Package Spec

## Purpose

`codemode` runs agent-authored code against a **resolved toolset snapshot**.

It is a session-like execution mode, but it is **not** the same thing as a service/session abstraction. A caller may use `codemode` directly with a resolved toolset, without any session object at all.

`codemode` owns:
- code-session semantics
- SDK shape generation from a resolved toolset
- execution of agent-authored code in a sandbox
- sequencing of repeated tool calls inside a code run
- aggregation of audit and result data across the run

`codemode` does **not** own:
- tool/package loading
- toolset assembly
- tool-call validation rules
- direct runtime execution semantics for tools
- transport policy
- remote/local service lifecycle
- long-lived capability/session registries

## Why this package exists

A plain tool invocation answers:

> Run this one tool with these params.

CodeMode answers:

> Give the agent a temporary SDK derived from the current toolset, let it explore, read, compute, and invoke tools multiple times, then return one final result plus a trace.

This is a different product concept from a single tool call, so it deserves a distinct package.

---

## Core idea

`codemode` consumes a **resolved toolset snapshot** and produces a **code execution result**.

It does not require a mutable session object.

A future `service/session` package may provide a convenient way to accumulate context and tool selections over time, but the final thing passed to `codemode` should be a resolved, immutable execution snapshot.

This spec now assumes two important product-level patterns:

1. **Discovery and action are different code modes.**
   - One code mode exists to discover capabilities, skills, examples, and the current visible SDK shape.
   - Another code mode exists to execute agent work against an immutable action snapshot.

2. **Rediscovery is the default response to change.**
   - If the caller wants to use the latest tool environment and that environment may have changed, the right default is to rerun discovery.
   - The primary control flow should not require the agent to interpret low-level diffs of bindings, CEL expressions, or hidden params.

---

## Dependencies

### Depends on `toolset`
Uses a resolved toolset snapshot as the source of truth for:
- what tools exist
- what names the agent sees
- what params are visible
- what hidden params will be resolved at call time

### Depends on `invoke`
Delegates every actual tool call to `invoke`.

This is a hard architectural rule:

> `codemode` must not call runtimes directly.

The meaning of “run a tool” should stay centralized in `invoke`.

### Depends on `audit`
Aggregates per-call audit data into a session-level trace.

---

## Non-goals

`codemode` should not:
- reimplement tool validation
- know about CEL semantics directly
- know whether the underlying tool runs in QuickJS or WASIX
- know how packages were fetched
- require service/session abstractions for local use
- decide when a caller should rediscover capabilities
- own the persistence layer for snapshot handles such as `toolboxID`

---

## Product-level operational modes

`codemode` is one package, but callers will often expose it through two distinct product surfaces.

### 1. Discovery code mode

Externally, this may be exposed as something like:

- `tool_discovery_execute`

It runs agent-authored code against a **discovery toolset**, not the full action surface.

The discovery toolset is expected to expose a compact introspection SDK for things like:
- available namespaces/capabilities
- visible method names and visible params
- skills / recipes
- examples
- current scope summaries
- current capability snapshot metadata

The discovery toolset should expose **summaries** of hidden binding effects, not raw hidden bindings.

Examples of good summaries:
- "Scoped to current customer"
- "Allowed channels only"
- "Some params are pre-bound and not agent-visible"

Examples of things discovery should usually not dump directly:
- raw secret material
- raw hidden param values
- low-level CEL expressions

### 2. Action code mode

Externally, this may be exposed as something like:

- `tool_action_execute`

It runs agent-authored code against an **immutable action snapshot**.

This is the code mode that sees the actual action SDK surface and performs real tool work.

The default behavior should be:
- discover first
- obtain an opaque toolbox snapshot handle
- act against that exact snapshot

---

## `toolboxID` as the handoff between discovery and action

At the product/API layer above `codemode`, the caller will often want an opaque handle that bridges discovery and action.

This spec uses:
- `toolboxID`

### Meaning of `toolboxID`

`toolboxID` should mean:

> An opaque identifier for an immutable discovered toolbox snapshot.

It should **not** mean:
- "whatever the latest toolbox is right now"
- a mutable session pointer
- a best-effort alias to current state

That distinction is important. If `toolboxID` is mutable, the visible SDK shape can drift underneath the model.

### Ownership boundary

`codemode` itself does not need to own ID issuance or persistence.

A higher layer may:
1. resolve the current discovery surface
2. resolve or materialize an immutable action snapshot
3. persist that snapshot under a `toolboxID`
4. pass the resolved snapshot into `codemode`

In other words:
- `codemode` runs against resolved snapshots
- a higher layer may expose those snapshots through `toolboxID`

### Default product flow

```text
tool_discovery_execute(...)
  -> runs discovery codemode on current discovery toolset
  -> returns capability info + toolboxID

tool_action_execute(toolboxID, ...)
  -> resolves toolboxID to immutable action snapshot
  -> runs action codemode on that snapshot
```

---

## Inputs

Conceptually, `codemode` needs:

### 1. Resolved toolset snapshot
An immutable execution-ready snapshot from `toolset`.

Contains, conceptually:
- visible tools
- hidden params and bindings
- context
- credential attachment / refs
- enough information for `invoke` to run a tool call correctly

### 2. Code payload
Agent-authored code.

Initially assume:
- TypeScript/JavaScript-like code
- evaluated in a sandboxed runtime
- code uses an SDK generated from the toolset

### 3. Code mode options
Examples:
- timeout
- memory limits
- max tool calls
- max audit event count
- whether read loops are allowed to continue after certain errors

### 4. Execution metadata
Examples:
- request id
- actor id / harness id
- trace correlation id
- mode (`discovery` or `action`)
- optional higher-level `toolboxID` for correlation

This is mostly for audit correlation and product-level tracing.

---

## Outputs

Conceptually, `codemode` returns:

### 1. Final result
The value returned by the code run.

### 2. Session trace
A structured trace of what happened during the code session, such as:
- code-level logs
- tool calls made
- tool call results/errors
- timing
- aggregate audit events

### 3. Session status
Examples:
- completed
- failed
- timed out
- aborted due to too many tool calls

### 4. Normalized error
If the run fails, `codemode` should normalize the failure into a session-level error instead of leaking runtime-specific details directly.

### 5. Optional product-level handoff data
When used in discovery mode via a higher layer, the final result will often include data such as:
- recommended capabilities
- skill references
- examples
- `toolboxID`

This handoff data is not owned by `codemode` as a package, but it is a common product pattern built on top of it.

---

## Conceptual model

There are four distinct things:

### A. Toolset snapshot
Immutable, execution-ready, resolved view.

Owned by `toolset`.

### B. Code session runtime
Temporary execution of agent code against the snapshot.

Owned by `codemode`.

### C. Tool invocation
Individual calls made from the session into the platform.

Owned by `invoke`.

### D. Snapshot handle (`toolboxID`)
Optional product-layer handle that refers to an immutable snapshot used by action execution.

Owned by the caller / higher-level API layer.

This separation is important. If `codemode` starts owning tool invocation semantics, it becomes a second execution engine. If `codemode` starts owning session/snapshot registries, it becomes a service layer.

---

## Pseudocode interface (conceptual)

This is not Go API yet. It is here to make the package boundary concrete.

### Minimal package mental model

```text
resolved_toolset = toolset.resolve(...)

code_run = codemode.prepare(
  toolset = resolved_toolset,
  code = agent_code,
  options = {...},
  metadata = {...},
)

result = code_run.execute()

return {
  value: result.value,
  trace: result.trace,
  status: result.status,
  error: result.error,
}
```

### Discovery product flow (conceptual)

```text
current_discovery_toolset = toolset.resolve(discovery_input)

discovery_result = codemode.execute(
  toolset = current_discovery_toolset,
  code = discovery_code,
  metadata = { mode: "discovery" },
)

higher layer materializes immutable action snapshot
higher layer stores it as toolboxID

return {
  toolboxID,
  discovery: discovery_result.value,
  trace: discovery_result.trace,
}
```

### Action product flow (conceptual)

```text
action_toolset = lookup_snapshot(toolboxID)

action_result = codemode.execute(
  toolset = action_toolset,
  code = action_code,
  metadata = {
    mode: "action",
    toolboxID,
  },
)

return action_result
```

### What happens internally

```text
codemode.prepare(...)
  -> derives SDK shape from resolved toolset
  -> creates sandbox session
  -> injects SDK into sandbox
  -> wraps each SDK tool call so it delegates to invoke
  -> creates trace collector
```

```text
sandboxed code calls:
  tools.tickets.get({ ticket_id: "123" })

codemode intercepts SDK call
  -> invoke.run(resolved_toolset, "tickets.get", params)
  -> receives normalized result + audit
  -> appends to session trace
  -> returns value into sandboxed code
```

### Important detail

The code sees a tool-shaped SDK, not raw `invoke`.

So from the agent’s point of view:

```text
const ticket = await tickets.get({ ticket_id: "123" })
```

But from the architecture’s point of view:

```text
SDK call -> codemode adapter -> invoke -> runtime -> result -> trace -> sandbox
```

---

## SDK generation

A central job of `codemode` is to generate the SDK shape the code sees.

### Input to SDK generation
A resolved toolset snapshot.

### Output of SDK generation
A callable SDK surface derived from the toolset’s agent-visible view.

Example action SDK:

If the toolset agent view contains:

```text
tickets.get(ticket_id)
tickets.comments.add(ticket_id, body)
tickets.list()
```

then the action code-mode SDK should feel like:

```text
tools.tickets.get(...)
tools.tickets.comments.add(...)
tools.tickets.list(...)
```

Example discovery SDK:

If the discovery toolset exposes capability introspection, the discovery code-mode SDK might feel like:

```text
capabilities.search(...)
capabilities.describe(...)
skills.search(...)
skills.get(...)
examples.forCapability(...)
```

The exact emitted shape is a `codemode` concern, not a `toolset` concern.

### Important rule

`toolset` defines **what is visible**.
`codemode` defines **how that visible shape is presented as code**.

---

## Discovery vs action behavior

### Discovery mode
Discovery mode should be optimized for:
- low prompt pressure
- fast re-grounding
- safe inspection of the current capability world
- progressive disclosure
- retrieving current visible names/params/examples/skills only when needed

### Action mode
Action mode should be optimized for:
- executing real work against a pinned snapshot
- stable SDK shape during the run
- predictable audit and replay semantics
- avoiding silent drift caused by toolset or binding changes elsewhere

### Recommended agent policy

The intended product-level policy is:

> Discover, obtain `toolboxID`, then act.

And when the caller wants the latest environment after any possible change:

> Rerun discovery.

This is preferable to teaching the agent to interpret low-level diffs of bindings or hidden params.

---

## Handling change

If the caller suspects the tool environment may have changed, the default recommendation should be:
- rerun discovery
- obtain a fresh `toolboxID`
- use that fresh `toolboxID` for future action execution

This spec intentionally does **not** make low-level diff interpretation the primary control flow.

Diffing may still exist as an optional introspection/debugging feature at a higher layer, but the main agent loop should not depend on it.

### Why
Because changes may involve:
- tools added or removed
- visible SDK shape changes
- hidden binding changes
- changed checks / scope
- docs/examples updates

The safe default is to re-ground on the current discovery surface, not to patch a stale mental model incrementally.

---

## Trace model

A CodeMode run should produce a session-level trace that is richer than a single tool audit record.

### Likely trace elements
- code session started
- mode identified (`discovery` or `action`)
- sdk generated
- tool call started
- tool call completed
- tool call failed
- code log emitted
- code returned value
- code timed out / aborted
- optional correlated `toolboxID`

### Why this matters
The entire value proposition of the product is introspection. CodeMode should therefore be designed with trace aggregation in mind from the beginning.

---

## Error model

Errors should be normalized at the CodeMode boundary.

Examples of internal failure causes:
- JS/TS syntax error
- sandbox runtime error
- tool invocation validation failure
- underlying runtime error from invoke
- timeout
- too many tool calls

These should become CodeMode-level outcomes such as:
- invalid_code
- runtime_error
- tool_call_failed
- timeout
- budget_exceeded

The exact taxonomy can be decided later, but the boundary should normalize these classes instead of leaking raw internals.

### Common product-level errors above `codemode`
A higher layer exposing `tool_action_execute` will likely also need errors like:
- missing_toolbox_id
- toolbox_not_found
- toolbox_expired
- toolbox_not_valid_for_context

Those errors are usually owned by the product/API layer, not by `codemode` itself.

---

## Limits / budgets

CodeMode likely needs explicit budgets that do not apply to ordinary tool invocation.

Examples:
- maximum wall time
- maximum number of tool calls
- maximum nested async work
- maximum output size
- maximum trace size

These are CodeMode concerns because they govern session semantics, not individual tool semantics.

A higher layer may also want different default budgets for discovery vs action:
- discovery: cheaper, safer, mostly read-oriented
- action: stricter budgets and stronger failure semantics

---

## What crosses the package boundary

### Into `codemode`
- resolved toolset snapshot
- code payload
- code mode options
- execution metadata

### Out of `codemode`
- final result value
- session-level trace
- normalized session status
- normalized error

### Across the internal `codemode -> invoke` boundary
Repeated single-tool invocation requests and results.

Conceptually:

```text
codemode session
  -> invoke one tool call
  -> get normalized result
  -> continue session
```

---

## Architectural invariants

1. `codemode` consumes resolved toolset snapshots, not mutable sessions.
2. `codemode` delegates tool execution to `invoke`.
3. `codemode` does not know runtime-selection rules directly.
4. `codemode` owns SDK presentation and session semantics.
5. `codemode` aggregates trace data; it does not invent per-tool execution semantics.
6. `codemode` may be used for both discovery and action, but those are product-level modes over different resolved toolsets.
7. `codemode` should not become the registry for `toolboxID` or other long-lived handles.

---

## Open questions

These should remain open for a spike rather than being prematurely locked down.

### 1. SDK form
Should the SDK be:
- hierarchical (`tools.tickets.comments.add`)
- flattened
- proxy-based / dynamic
- generated code

Potentially the answer differs between discovery and action modes.

### 2. Discovery SDK shape
How structured should the discovery surface be?

Examples:
- `capabilities.search(...)`
- `capabilities.describe(...)`
- `skills.get(...)`
- `examples.forCapability(...)`

### 3. Session state inside code mode
Does the code runtime need mutable scratch state or helpers beyond plain JS variables?

### 4. Streaming vs polling
The package should not depend on streaming semantics, but local callers may still want incremental access to trace events.

### 5. Read vs write ergonomics
Should discovery and action have clearly different runtime defaults and budgets, especially around read-heavy loops vs write operations?

### 6. Snapshot lifetime policy
How long should higher layers retain immutable snapshots behind `toolboxID` before requiring rediscovery?

---

## Summary

`codemode` is best understood as:

> A session-oriented execution layer that runs agent-authored code against a resolved toolset snapshot, while delegating every actual tool call to `invoke` and aggregating the resulting trace.

At the product layer, that same package can power two distinct surfaces:
- **discovery code mode** (`tool_discovery_execute`) for capability/skill/example inspection and progressive disclosure
- **action code mode** (`tool_action_execute`) for real work against an immutable snapshot referenced by `toolboxID`

That keeps the boundary clean:
- `toolset` decides what exists and what is visible
- `invoke` decides how one tool call executes
- `codemode` decides how agent code interacts with the toolset over time
- a higher layer may issue `toolboxID` handles and require rediscovery when the environment changes
