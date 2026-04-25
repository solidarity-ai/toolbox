# Tool Call Plan

We are moving approval and long-lived uncertainty to the **tool call layer**, not the cell or background-code layer. In v1 we do **not** add spawned background code execution, AST splitting, closure capture analysis, or hidden task runtimes. Instead, every tool invocation gets a durable `toolCallId`, and the JS surface exposes that durable identity directly.

The result is a simple model:

- no tool returns bare `T`
- tools either return `ToolCallPromise<T>` or `ToolCallTask<T>`
- both forms expose a durable `toolCallId`
- `$tool_call(...)` is the single inspection surface
- long-lived approval/restart complexity is handled at the **tool call** level

This keeps the notebook linear, keeps replay understandable, and avoids introducing a second workflow engine for user-authored code.

## JS surface

```ts
type ToolCallTask<T = unknown> = {
  toolCallId: string
}

type ToolCallPromise<T> = Promise<T> & {
  toolCallTask: ToolCallTask<T>
}

type ToolCallView<T = unknown> =
  | {
      toolCallId: string
      toolName: string
      status: "started"
      params?: unknown
    }
  | {
      toolCallId: string
      toolName: string
      status: "needsApproval"
      params?: unknown
      approval?: { approvalId?: string }
    }
  | {
      toolCallId: string
      toolName: string
      status: "success"
      params?: unknown
      result: T
    }
  | {
      toolCallId: string
      toolName: string
      status: "failed"
      params?: unknown
      error: unknown
    }
  | {
      toolCallId: string
      toolName: string
      status: "cancelled" | "unknown"
      params?: unknown
    }

declare function $tool_call<T>(
  refOrId: ToolCallTask<T> | string
): ToolCallView<T>
```

## Requirements

1. **Every tool call gets a durable identity**
   - Every tool invocation MUST allocate a stable durable `toolCallId`.
   - That `toolCallId` is the canonical identity for inspection, replay, restore, and restart recovery.
   - A returned JS object that refers to a tool call MUST always point at a real durable tool call record.

2. **No bare `T` return values**
   - No tool may return bare `T` directly.
   - A tool MUST return either:
     - `ToolCallPromise<T>`, or
     - `ToolCallTask<T>`.
   - This rule is required so every tool call has uniform inspectability and a uniform durable identity.

3. **Two runtime categories of tool return**
   - Tools whose calls are allowed to settle inline in the current cell MUST return `ToolCallPromise<T>`.
   - Tools whose calls must not suspend the current cell, may require approval, or may outlive the current cell MUST return `ToolCallTask<T>`.
   - This classification is an SDK/runtime contract, not a best-effort heuristic.

4. **Conservative tool classification**
   - If a tool call could validly require approval or otherwise become long-lived, that tool MUST be classified as returning `ToolCallTask<T>`.
   - Classification MUST be conservative and stable for the session.
   - A tool MUST NOT switch between `ToolCallPromise<T>` and `ToolCallTask<T>` dynamically per call in v1.

5. **Durable start before JS return**
   - Before any `ToolCallPromise<T>` or `ToolCallTask<T>` is returned to JS, the runtime MUST durably record tool-call start.
   - If durable start recording fails, the cell MUST fail and no JS handle may be returned.
   - Returning a handle before durable start is forbidden.

6. **`ToolCallTask<T>` shape**
   - `ToolCallTask<T>` MUST be plain JS data.
   - Minimum shape: `{ toolCallId: string }`.
   - It MUST be jswire-serializable.
   - It MUST be safe to store in variables, arrays, objects, `$last`, `$val(n)`, and committed cell results.
   - It MUST be replayable across reopen, restore, and restart.

7. **`ToolCallTask<T>` is never awaitable**
   - `ToolCallTask<T>` MUST NOT implement Promise-like or thenable behavior.
   - It MUST NOT have `.then`, `.catch`, or `.finally` semantics.
   - `await someToolCallTask` MUST NOT become a hidden blocking mechanism.

8. **`ToolCallPromise<T>` shape**
   - `ToolCallPromise<T>` MUST be awaitable from JS and MUST expose `.toolCallTask` immediately.
   - `toolCallTask` MUST be available before the promise settles.
   - This MUST work:

   ```ts
   const p = someTool(...)
   const task = p.toolCallTask
   const value = await p
   ```

9. **`ToolCallPromise<T>` shares the same durable record**
   - The `toolCallTask` attached to a `ToolCallPromise<T>` MUST refer to the same durable tool call that the awaited promise result came from.
   - Awaiting the promise MUST NOT create a second hidden tool call.
   - `$tool_call(p.toolCallTask)` MUST inspect the same call the promise is waiting on.

10. **No foreground suspension for `ToolCallTask<T>` tools**
    - A tool that returns `ToolCallTask<T>` MUST NOT suspend the current cell waiting for approval or completion.
    - The cell may commit normally after receiving the task handle if everything else succeeds.
    - The main notebook session MUST remain linear and MUST NOT introduce partial-cell commit semantics to support these tools.

11. **Inline-settling tools still use durable records**
    - A tool that returns `ToolCallPromise<T>` may still complete quickly and inline.
    - Even for these tools, the system MUST durably preserve the tool call record and final outcome so `$tool_call(...)` remains meaningful later.

12. **Durable tool-call lifecycle model**
    - Each `toolCallId` MUST have explicit durable lifecycle state.
    - Minimum states:
      - `started`
      - `needsApproval`
      - `success`
      - `failed`
      - `cancelled`
      - `unknown`
    - `unknown` is mandatory for crash/restart ambiguity.

13. **Durable tool-call metadata**
    - Each durable tool-call record MUST link at least:
      - `toolCallId`
      - owning session identity
      - owning branch/head/cell context
      - tool name
      - normalized params snapshot
      - status
      - approval metadata when relevant
      - success result snapshot when available
      - failure snapshot when available
    - If timestamps are stored, they SHOULD include started and last-updated times.

14. **Approval metadata**
    - When a tool call is waiting for approval, durable state MUST expose machine-readable approval information.
    - Minimum approval information:
      - `status: "needsApproval"`
      - optional provider approval identifier
      - tool name
    - Lack of a provider approval id MUST NOT block representation of approval-pending state.

15. **Readonly inspection API**
    - Expose `$tool_call(refOrId)` in JS.
    - It MUST accept either a `ToolCallTask<T>` or a raw `toolCallId` string.
    - It MUST be a readonly, non-blocking host query.
    - It MUST return immediately with a snapshot of durable known state.
    - It MUST NOT long-poll, wait for approval, or wait for completion.

16. **Structured inspection result**
    - `$tool_call(...)` MUST return a machine-readable JS object.
    - Minimum common fields:
      - `toolCallId`
      - `toolName`
      - `status`
    - Additional fields by status:
      - `params` when available
      - `approval` when `needsApproval`
      - `result` when `success`
      - `error` when `failed`
    - `unknown` MUST be first-class, not collapsed into failure.

17. **Replay semantics for inspection**
    - A cell that calls `$tool_call(...)` MUST record the snapshot it observed.
    - On replay/restore of that same cell, `$tool_call(...)` MUST replay the same observed snapshot.
    - Later cells may observe newer snapshots for the same `toolCallId`.
    - This is required so notebook history remains stable while tool calls continue evolving.

18. **Replay semantics for initiating cells**
    - If a committed cell originally created a tool call and obtained either `ToolCallPromise<T>.toolCallTask` or a direct `ToolCallTask<T>`, replay/restore of that cell MUST reuse the same `toolCallId`.
    - Replay/restore MUST NOT create a duplicate external tool call.
    - This applies across reopen, restore, restart recovery, and branch replay.

19. **Inherited history semantics**
    - If committed history contains a stored `ToolCallTask<T>`, any branch or restore that inherits that history MUST see the same `toolCallId`.
    - `$tool_call(...)` against that inherited handle MUST inspect the same durable underlying tool call.
    - Tool-call identity is part of notebook history, not transient runtime state.

20. **Out-of-band completion**
    - After start has been durably recorded, the underlying tool call may complete:
      - immediately,
      - later,
      - after approval,
      - after reconnect,
      - or after process restart.
    - The completion path MUST be decoupled from the originating MCP request lifetime.

21. **Restart recovery**
    - Non-terminal tool calls MUST survive process restart.
    - On restart, the system MUST recover durable tool-call state and continue to surface it through `$tool_call(...)`.
    - Recovery MUST support at least:
      - approval still pending,
      - terminal outcome already known,
      - outcome not yet knowable,
      - and explicit `unknown` state when ambiguity remains.

22. **Unknown-state handling**
    - If the system cannot prove whether a started tool call succeeded or failed, it MUST report `status: "unknown"`.
    - It MUST NOT invent success.
    - It MUST NOT silently rerun dangerous calls just to collapse uncertainty.
    - `unknown` is a valid durable state.

23. **Single inspection surface**
    - V1 SHOULD use `$tool_call(...)` as the single authoritative inspection API.
    - Future sugar like `$tool_result(...)` MAY be added later, but MUST be derived from the same durable tool-call record and MUST NOT introduce a second truth source.

24. **Ownership and access control**
    - `toolCallId`s MUST be scoped to the owning session lineage.
    - `$tool_call(...)` MUST reject malformed, unknown, or out-of-scope ids.
    - V1 MUST fail closed rather than exposing unrelated tool calls across sessions.

25. **Type-level enforcement**
    - Generated TypeScript declarations MUST make the tool surface honest:
      - `ToolCallPromise<T>` where inline await is supported
      - `ToolCallTask<T>` where inline await is not supported
    - The type surface MUST make misuse visible:
      - no pretending `ToolCallTask<T>` is `T`
      - no property access on final result without first awaiting a promise or inspecting the task
      - no accidental straight-line chaining through long-lived calls

26. **Runtime enforcement**
    - Even if user code escapes the type system with `as any`, runtime semantics MUST remain coherent.
    - A `ToolCallTask<T>` tool MUST still return a plain task handle, never the final value.
    - A `ToolCallPromise<T>` tool MUST still return an awaitable object with `.toolCallTask`, not a bare `T`.

27. **Loop and control-flow compatibility**
    - Because `ToolCallTask<T>` is immediate and plain data, loops and conditionals MUST work without special transforms.
    - Supported patterns MUST include:
      - arrays of `ToolCallTask<T>`
      - maps keyed to `ToolCallTask<T>`
      - collecting many pending calls in loops
      - later inspecting each with `$tool_call(...)`
    - This is a core reason to prefer tool-call handles over code-level background tasks in v1.

28. **Cell commit behavior**
    - A cell that only starts one or more `ToolCallTask<T>` calls and stores their handles MUST commit normally if everything else succeeds.
    - A cell using `ToolCallPromise<T>` behaves like ordinary inline-await code and commits only if its awaited work settles successfully under existing session semantics.
    - No special approval-driven partial-cell machinery is introduced.

29. **No code-task layer in v1**
    - V1 MUST NOT introduce:
      - background spawned code execution,
      - hidden code tasks,
      - AST splitting,
      - closure capture analysis,
      - or a second replay layer for user-authored code.
    - The only long-lived durable async unit introduced here is the **tool call**.

30. **No separate workflow DSL in v1**
    - V1 MUST NOT require `Task`, `ToolOp`, `MaybeApprovalPromise`, or callback-scoped workflow types beyond `ToolCallPromise<T>` and `ToolCallTask<T>`.
    - The design intentionally stays at the tool-call abstraction boundary.

31. **Canonical execution path**
    - This design MUST NOT introduce a second independent tool execution engine.
    - Tool-call start, continuation, completion, and failure MUST still flow through the platform’s canonical tool invocation path.
    - The new work here is durable identity, return-shape semantics, and inspection, not a second executor.

32. **Instructions and SDK docs**
    - Session instructions and generated SDK text MUST clearly explain:
      - every tool call has a `toolCallId`
      - `ToolCallPromise<T>` exposes `.toolCallTask`
      - `ToolCallTask<T>` is inspected with `$tool_call(...)`
      - long-lived/approval-capable calls do not yield final values inline
    - This guidance is required so LLM behavior matches the model.

33. **Notifications are advisory only**
    - Client notifications for tool-call updates MAY exist.
    - They MUST NOT be authoritative.
    - `$tool_call(...)` over durable tool-call records is the authoritative disconnect-safe interface.

## Short summary

The v1 plan is to make **tool calls** the single durable async abstraction. Every tool invocation gets a durable `toolCallId`. Tools return either `ToolCallPromise<T>` for inline-settling calls or `ToolCallTask<T>` for long-lived calls that must not suspend the current cell. Both forms expose the same underlying durable tool-call identity, and `$tool_call(...)` is the single readonly inspection API for status, approval, success, failure, cancellation, and unknown recovery states. This avoids introducing background code-task replay, keeps notebook replay stable, and keeps restart recovery focused on **tool calls**, not user-authored code execution.