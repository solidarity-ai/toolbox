# `toolset` — Design Document

## Purpose

Assembles a request-scoped execution environment from tool definitions, bindings, context, and credentials. This is the central package — everything downstream receives a resolved toolset.

## Core Types

### Toolset

The top-level type. Assembled per-request by the harness. Immutable once created.

Contains:

- A list of bound tools
- A context object (key-value, from the harness)
- A credential set (named secrets)

### BoundTool

A reference to a tool definition + bindings for its params.

Contains:

- Fully qualified tool reference (package + tool name + version, e.g. `zendesk@2.0.1/account.tickets.comments.add`)
- A map of param name → Binding
- Every param in the tool definition MUST have a binding (no implicit passthrough)
- Selector params declared by the package resource tree are params like any other — they may be bound too

### Binding

Per-param. Describes where the value comes from and whether the agent sees it.

Contains:

- `value` — a CEL expression with access to `params` and `context`. Examples:
  - Literal string: `"'#engineering'"`
  - Literal number: `"100"`
  - Context reference: `"context.customer_id"`
  - Param passthrough: `"params.channel"` (agent provides this)
  - Computed: `"context.base_url + '/api/v1'"`
- `hidden` — bool, default false. If true, param is not visible to the agent.
- `check` — optional CEL expression returning bool. Evaluated before execution. Has access to `params` and `context`. Example: `"params.channel in context.allowed_channels"`

CEL is the one language used everywhere. `value` resolves the param. `check` validates it. Both use the same namespace: `params` (agent-provided values) and `context` (harness-provided values).

### Resource-Level Binding

Because packages declare resource selectors once, binding a selector's canonical
`binding_name` applies to every tool that consumes that selector.

Binding `account` once can scope every tool that selects `account`; collection
methods that do not consume the selector are unaffected.

### Context

A flat key-value map. String keys, any values. Set by the harness. Available to all CEL expressions as `context`.

Examples: `customer_id`, `environment`, `user_role`, `session_id`, `allowed_channels`

### CredentialSet

A map of credential name → secret value. Used by the transport layer. Not visible to tools or agents.

## Operations

### Resolve

Takes a Toolset (with unresolved CEL expressions) and produces a ResolvedToolset where:

- All `value` CEL expressions are compiled (ready to evaluate per-request)
- All `check` expressions are compiled (ready to evaluate per-call)
- Resource-level bindings are propagated to all tools under that resource
- Agent-visible param schemas are generated (hidden params removed)
- Agent-visible tool names are adjusted (bound resource levels are stripped from the path)
- Static values (literals) are pre-evaluated
- Errors reported for: invalid CEL syntax, context references to missing keys, type mismatches

### AgentView

Takes a ResolvedToolset and produces the agent-facing tool listing:

- Only tools in the toolset
- Only non-hidden params per tool
- Tool names shortened by stripping bound resource levels
- Param schemas narrowed to what the agent can actually provide
- Tool metadata (readOnly, destructive, etc.) passed through

### ValidateCall

When an agent calls a tool with params:

1. Look up the BoundTool in the resolved toolset
2. For each binding: evaluate the `value` CEL expression (with agent-provided params + context)
3. For each binding with a `check`: evaluate it against params + context. Fail → policy violation.
4. Return the full param set (hidden + visible, all resolved) or an error.

## What This Package Does NOT Do

- Does not execute tools (that belongs to `invoke`)
- Does not make HTTP calls (that belongs to `transport`)
- Does not store or retrieve tool definitions (that belongs to `registry`)
- Does not own protocol translation or code-session execution (that belongs to `service` and `codemode`)
- Does not expose CEL as a public package boundary; CEL compilation and evaluation stay internal to toolset assembly
- Does not handle tool visibility logic — if a tool is in the toolset, it's visible. If it's not, it's not. The harness decides.
- Does not define the tool/package format — that's the tool definition spec. This package consumes it.

## Example

A harness sets up a toolset for a customer support agent using tools from `zendesk@2.0.1` and `slack@1.2.0` packages:

```
Toolset:
  context:
    customer_id: "acme-123"
    environment: "production"
    agent_role: "support"
    allowed_channels: ["#support", "#escalation"]

  credentials:
    slack_token: "xoxb-..."
    zendesk_key: "zd_..."

  resource_bindings:
    account_id: { value: "context.customer_id", hidden: true }

  tools:
    - tool: slack@1.2.0/channels.messages.send
      bindings:
        channel_id:   { value: "params.channel_id", hidden: false,
                        check: "params.channel_id in context.allowed_channels" }
        message:      { value: "params.message", hidden: false }

    - tool: zendesk@2.0.1/account.tickets.get
      bindings:
        ticket_id:    { value: "params.ticket_id", hidden: false }

    - tool: zendesk@2.0.1/account.tickets.comments.add
      bindings:
        ticket_id:    { value: "params.ticket_id", hidden: false }
        body:         { value: "params.body", hidden: false }

    - tool: zendesk@2.0.1/account.tickets.list
      # no extra bindings needed — account_id covered by resource binding
```

Note: `account_id` binding is declared once at resource level and applies to all `zendesk@2.0.1/account.*` tools automatically.

Canonical binding names are authored in the package resource declaration; they
are not inferred from filenames.

Agent sees (bound resource levels stripped from names):

```
Available tools:
  channels.messages.send(channel_id: string, message: string)
  tickets.get(ticket_id: string)
  tickets.comments.add(ticket_id: string, body: string)
  tickets.list()
```

Agent calls `tickets.comments.add({ ticket_id: "12345", body: "On it" })`:

1. ValidateCall looks up `zendesk@2.0.1/account.tickets.comments.add` in toolset
2. Resolves resource binding: account_id → "acme-123" (from context.customer_id)
3. Resolves method bindings: ticket_id → "12345", body → "On it"
4. No check expressions to evaluate
5. Returns full params: `{ account_id: "acme-123", ticket_id: "12345", body: "On it" }`

Agent calls `channels.messages.send({ channel_id: "#general", message: "hello" })`:

1. Evaluates check on channel_id: `"#general" in ["#support", "#escalation"]` → **fail**
2. Returns policy violation error

## Code Mode Interaction

When code mode receives a resolved toolset, it generates an SDK where bound resource levels become the entry point:

```typescript
// Agent writes this in code mode:
const tickets = await zendesk.tickets.list();
for (const t of tickets) {
  const comments = await zendesk.tickets.comments.list({ ticket_id: t.id });
  // ...
}
await zendesk.tickets.comments.add({ ticket_id: "12345", body: "Done" });
```

The SDK shape is derived from AgentView — the same shortened tool names become method paths.

## Dependencies

- `tool` — imports Tool, Param, Metadata types
- internal CEL compilation/evaluation helpers used during resolution

## Depended on by

- `invoke` — receives a resolved toolset and calls `ValidateCall` before execution
- `codemode` — receives a resolved toolset and generates an SDK from `AgentView`
- `service` — receives toolset inputs from a harness or protocol request, calls `Resolve`, and serves `AgentView`
