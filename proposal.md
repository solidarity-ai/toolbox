# Mission Proposal: Prepared-Tool REPL Integration for CodeMode

## Plan Overview
- Replace codemode's SDK generator with a prepared-tools pipeline that invokes `super_tool(sessionId, tsSource)` so the repljs super_tool becomes the canonical execution path for both CLI and MCP harnesses, ensuring prepared tools still drive SDK generation and `$tool_call(effectId)` semantics remain canonical across surfaces.
- Deliver a TypeScript-only CLI REPL whose colon commands include `:help`, `:exit`, `:repl new`, and `:repl switch <id>`, whose default prompt enumerates `$tools`, and which maintains a per-codemode-session SDK hash so every REPL session blocks execution until a prompt refresh follows any tool/context change.
- Scope storage to each `toolbox --codemode` run by allowing the operator to pick a storage engine at CLI startup (default SQLite), while persisting per-directory SQLite files so codemode sessions launched inside the same working directory reuse a shared DB file without leaking across directories, and still tracking the codemode-session hash for prompt gating.
- Provide a `$docs(identifier)` host function that regenerates documentation in memory on demand (without writing files) and returns a map of `toolId -> documentation string` via the cell result, while session-scoped host functions expose `$tool_call(effectId)` inspectors and structured failure telemetry across CLI and MCP surfaces.

## Expected Functionality & Milestones
1. **Prepared-tool wiring & entry points**: Route `toolbox --codemode` sessions through the repljs `super_tool(sessionId, tsSource)` prepared tool, ensure CLI and MCP bootstraps share the same toolset snapshot, and make this the exclusive execution hook for CodeMode TypeScript snippets.
2. **Prepared-tool SDK & `$docs` regeneration**: Replace the current codemode generator with a prepared-tools-based SDK builder; `$docs(identifier)` should return a map of `toolId -> documentation string` generated entirely in-memory, discarding any prior artifacts so nothing stale is reused.
3. **REPL loop semantics & prompt gating**: Keep the REPL TypeScript-only, support `:help`, `:exit`, `:repl new`, and `:repl switch <id>`, show `$tools` in the default prompt, and maintain a codemode-session SDK hash that blocks every REPL session’s cell execution until a prompt refresh occurs after any bound tool/context change.
4. **Session storage & persistence**: Let the CLI select the storage engine at runtime, defaulting to SQLite with files scoped per directory so codemode sessions in the same directory share a single DB file, while codemode-session hash tracking ensures prompt gating still reflects the active toolset.
5. **Host functions, logging & failure envelopes**: Install host functions per session so every prepared-tool call can be inspected via `$tool_call(effectId)` (analogous to `$val(N)`); include params/results snapshots with `success`, `started`, or `unknown` statuses inside failure objects shared with CLI, MCP, and audit sinks so LLMs see which calls completed versus remain unknown.
6. **MCP mirroring & validation scaffolding**: Mirror the CLI semantics in `codemodemcp`, expose matching host/log surfaces (including `$docs` maps and `$tool_call` inspectors), and document the validation budget (up to 6 concurrent Go-test validators and up to 6 simultaneous CLI REPL tuistory sessions) for final verification.

## Environment Setup
- Maintain Go 1.22 toolchains (per `go.mod`) via `mise`, ensuring `go work sync` brings `codemode`, `codemodemcp`, `invoke`, and prepared-tool registries into the workspace.
- Ensure prepared tool definitions for repljs and dependent packages are locally available so SDK generation can rely solely on prepared metadata and the codemode-session hash can be derived consistently across CLI and MCP.
- Provide SQLite libraries compatible with CGO and confirm alternate storage engines can be injected at CLI startup without extra build tags; SQLite DB files are scoped per directory so codemode sessions opened from the same directory reuse the shared DB.
- Keep the QuickJS/TypeScript runtime assets that codemode expects, plus session-scoped scratch areas for ephemeral TypeScript generation even though `$docs` itself returns only in-memory maps.
- Install tuistory for manual CLI verification and confirm MCP binaries are runnable in the same environment.

## Infrastructure & Boundaries
- `codemode` continues to own session orchestration while delegating every tool execution to `invoke`; prepared tools merely inform SDK shape and repljs wiring.
- Storage abstractions remain session-bound, with SQLite (or alternates) initialized per directory so codemode sessions opened from the same path share a single DB file while other directories stay isolated; codemode-session hashes continue to gate REPL execution when tool/context changes occur.
- `$docs` outputs are generated entirely in memory as maps of `toolId -> documentation string`, eliminating on-disk artifacts while relying on the same prepared-tool metadata to keep the SDK and docs aligned.
- Host functions stay per-session, exposing `$tool_call(effectId)` so sessions can re-inspect the parameters/results/status of any tool invocation while keeping failure objects structured without bypassing existing audit/tracing systems.
- MCP remains a transport reflection of the CLI; no new business logic is added outside the codemode/invoke boundary, but MCP must also surface the per-codemode-session hash and multi-REPL semantics.

## Testing Strategy
- Expand automated Go tests across `codemode`, `codemodemcp`, and any storage modules to cover prepared-tool SDK generation, `$docs` map regeneration, prompt gating driven by the codemode-session hash, host function logging, and storage-engine selection.
- Simulate prepared-tool or context changes and ensure cells across every REPL session are blocked until prompts refresh, verifying both CLI and MCP handlers plus the `:repl new`/`:repl switch` flows.
- Exercise SQLite-backed sessions (shared per directory) plus at least one alternate engine stub in tests to confirm swap-ability and correctness of session hashing.
- CI may run up to six validators in parallel (`go test ./codemode/... ./codemodemcp/... ./invoke/...`) to match the allowed concurrency budget.

## User Testing Strategy
- Run tuistory-driven CLI sessions to confirm the REPL is TypeScript-only, that `:help`, `:exit`, `:repl new`, and `:repl switch <id>` behave as expected, and that default prompts show `$tools` while enforcing tool-change gating via the codemode-session hash.
- Execute `$docs(identifier)` during manual sessions to verify the returned map of documentation strings is produced purely in memory, with no artifacts written to disk.
- Rehearse storage-engine selection (e.g., SQLite vs. injected alternative) and ensure SQLite files are shared per directory while `$tool_call(effectId)` reports return the correct status metadata for completed/unknown calls.
- Cap simultaneous tuistory sessions at six during validation to align with the resource budget.

## Validation Readiness
- The dry run already succeeded; final readiness requires rerunning the Go test suite (with up to six concurrent validators) and replaying up to six manual tuistory sessions that cover CLI, `$docs`, prompt gating via the codemode-session hash, multi-REPL commands, storage selection, and MCP mirroring.
- Validation checklists must confirm: prepared-tool SDK generation replacement, default prompt enforcement, session-scoped storage persistence with per-directory SQLite files, `$docs` map semantics, `$tool_call` inspection hooks, and failure envelopes with status fields.

## Non-Functional Requirements
- Preserve session isolation, making sure persisted SQLite files are namespaced per directory and cannot leak data between directory-scoped codemode sessions.
- Keep `$docs` regeneration deterministic and side-effect free by returning only in-memory documentation maps instead of writing artifacts to disk.
- Ensure `$tool_call(effectId)` inspection plus failure envelopes are structured, machine-readable, and consistent across CLI/MCP with clear success/started/unknown annotations.
- Enforce prompt gating via the per-codemode-session hash to avoid executing code against out-of-date tool bindings, improving safety and predictability.
- Maintain accessibility: the CLI must remain usable in standard terminals/tuistory, and MCP calls should stay stateless aside from their session identifiers.

## Pending Questions
- None; all clarifications have been incorporated into this proposal.
