# Package: Stateless API View

**Feature ID**: 001
**Created**: 2026-03-23
**Frame**: [frame.md](frame.md)
**Status**: Shaping

---

## Problem

There is no shared, binding-aware function that produces the agent-visible tool surface. `toolset.Resolve()` passes through raw tools with no bindings or context. Codemode hardcodes SDK shape generation as an internal detail. MCP has no filtered view. Sessions have no foundation.

## Appetite

Big Batch: 6 weeks

## Requirements (R)

| ID | Requirement | Status |
|----|-------------|--------|
| R0 | Stateless function: `(packages, bindings, context) -> AgentView` | **Fully met** — `Builder.Resolve(Config)` → `ResolvedToolset` → `.AgentView()`. Stateless, deterministic. |
| R1 | CEL binding engine — value expressions resolve params from context/agent input | **Fully met** — `toolset/cel.go`: `newCELEnv` with `params`+`context` vars, `compileBinding`, `evalBinding`. cel-go v0.27.0. |
| R2 | Hidden params — bindings can hide params from agent, injected at call time | **Fully met** — `Binding.Hidden` removes from AgentView ParamsSchema+required. `ValidateCall` injects hidden values. 2 tests. |
| R3 | Check expressions — CEL guards validate agent params before execution | **Fully met** — `Binding.Check` compiled at resolve time, evaluated in `ValidateCall`. Returns `"check failed"` error. 2 tests. |
| R4 | Two-tier resource binding — convention-based from resource paths + explicit manifest override; toolset-level binds canonical names to context | **Partially met** — Convention inference (`InferResourceParams`) and canonical name propagation work. Manifest `resource_bindings` override field not yet added to `DevManifestTool`. |
| R5 | AgentView type — shared output with TS type info, consumed by MCP and codemode | **Partially met** — `AgentView`/`AgentTool` types exist and are consumed by codemode+service. `ParamTypes`/`ReturnType` fields not yet added (blocked on typescript-go fork extension). |
| R6 | Refactor codemode to consume AgentView instead of inline SDK generation | **Partially met** — `preludeForTools` uses `AgentView`. `typecheckSDKSource` still uses import-based approach (needs `ParamTypes` from R5). |
| R7 | Wire MCP service to serve AgentView as tool list | **Fully met** — `ToolsetService.ExecuteDiscovery` returns AgentView tools. `ExecuteAction` runs code via codemode. 2 tests. |
| R8 | ValidateCall — evaluate bindings + inject hidden params at invoke time | **Partially met** — `ResolvedToolset.ValidateCall` exists and works (6 tests). Not yet wired into `invoke.Run` as the call-site integration point. |

## Solution

Extract a shared `AgentView` from `toolset` that carries the agent-visible tool surface including TypeScript type information. Build a CEL binding engine internal to `toolset` that evaluates bindings, hides params, and validates calls. Extend `typescript-go/toolbox` to extract per-param TS types and return types. Refactor codemode to consume AgentView for SDK generation. Wire MCP service to serve AgentView as a tool list.

### Element: Binding Types

**What**: New types in `toolset` for bindings, bound tools, and resolve configuration.
**Where**: `toolset/toolset.go` — new types alongside existing `Builder` and `ResolvedToolset`
**Wiring**: `Config` is the input to an enhanced `Resolve()`. `Binding` is evaluated by the CEL engine.
**Affected code**: `toolset/toolset.go`
**Status**: Validated

```go
type Binding struct {
    Value  string // CEL expression: "context.customer_id", "params.channel", "'literal'"
    Hidden bool   // If true, agent never sees this param
    Check  string // Optional CEL guard: "params.channel in context.allowed_channels"
}

type BoundTool struct {
    ToolRef  string             // "zendesk@2.0.1/account.tickets.get"
    Bindings map[string]Binding // param name -> binding
}

type Config struct {
    Tools            []BoundTool        // Explicitly bound tools
    ResourceBindings map[string]Binding // Resource-level bindings (e.g., account_id)
    Context          map[string]any     // Flat key-value context from harness
    Credentials      map[string]string  // Named secrets (transport layer, not visible to tools)
}
```

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `Binding` | struct | CEL engine (compilation + evaluation) | Per-param resolution at call time |
| `BoundTool` | struct | `Resolve()` | Associates tool ref with param bindings |
| `Config` | struct | `Builder.Resolve(Config)` | Full resolve input from harness |

---

### Element: CEL Engine (internal to toolset)

**What**: Internal CEL evaluator. Compiles binding expressions at resolve time, evaluates at call time. CEL stays an implementation detail of `toolset` — not a public package.
**Where**: New file `toolset/cel.go` (unexported functions only)
**Wiring**: Called by `Resolve()` to compile `Binding.Value` and `Binding.Check` expressions. Called by `ValidateCall()` to evaluate at invoke time.
**Affected code**: New file `toolset/cel.go`, `go.mod` (add `github.com/google/cel-go`)
**Status**: Validated — cel-go v0.27.0 API confirmed. `cel.NewEnv` with `params` + `context` variables, `Compile()` at resolve time, `Program.Eval()` at call time.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `newCELEnv()` | internal func | cel-go library | `*cel.Env` with params + context variables |
| `compileBinding(env, expr)` | internal func | `Resolve()` | Compiled `cel.Program` or error |
| `evalBinding(prog, params, ctx)` | internal func | `ValidateCall()` | Resolved value or error |
| `evalCheck(prog, params, ctx)` | internal func | `ValidateCall()` | bool (pass/fail) or error |

---

### Element: Resource Param Inference + Package-Level Binding Names

**What**: Convention-based resource param inference from tool entry filenames. Package manifest can override with explicit binding name mapping when conventions don't fit across packages.
**Where**: `packaging/internal/manifest/manifest.go` (near existing `InferAccessMode`/`InferToolName`)
**Wiring**: `InferResourceParams(entryTS)` called during `Compile()` to populate new fields on `PackageTool`. Toolset reads these during `Resolve()` to know which params are bindable at resource level.
**Affected code**: `packaging/internal/manifest/manifest.go`, `tool/tool.go` (new fields on `PackageTool`)

**Convention**: `users.calendars.events.list.ts` -> infers `user_id`, `calendar_id` as resource params (strip trailing 's', append '_id'). `list` method doesn't require deepest resource ID (`event_id`); `get`/`update`/`delete` do.

**Manifest override**: Tool entries can declare `resource_bindings` to map inferred param names to canonical binding names:
```json
{
  "tools": [{
    "entry_ts": "tools/account.tickets.list.ts",
    "resource_bindings": { "account_id": "zendesk_account" }
  }]
}
```

Without override, inferred names are used directly (e.g., `account_id` stays `account_id`).

**Two-tier flow**:
1. Package declares: "my `account_id` param maps to binding name `zendesk_account`"
2. Toolset-level config binds: `zendesk_account -> { value: "context.customer_id", hidden: true }`
3. At resolve time, all `account.*` tools get `account_id` injected from context

**Status**: Validated — `InferToolName` already parses filenames the same way; this extends that pattern. The singularization rule (strip 's') is acknowledged as imperfect but functional per the tool definition spec.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `InferResourceParams(entryTS)` | func | `Compile()` | `[]ResourceParam{Name, BindingName}` |
| `PackageTool.ResourceParams` | new field | `toolset.Resolve()` | Param name + canonical binding name pairs |
| `DevManifestTool.ResourceBindings` | new field | `Compile()` | Optional name overrides from manifest |

---

### Element: Extended Tool Metadata Extraction (typescript-go fork)

**What**: Extend `ExtractToolMetadata` in the `typescript-go/toolbox` package to return per-parameter TS type strings and the function return type, in addition to JSON Schema. This gives AgentView enough info for codemode to emit typed SDK declarations without importing tool source modules.
**Where**: `/home/mackross/dev/vendor/typescript-go/toolbox/extract.go` — extend `ToolMetadata` struct and `ExtractToolMetadata` function
**Wiring**: Called by `packaging/internal/source/source.go:enrichToolMetadata()`. New fields stored on `PackageTool` and passed through to `ResolvedTool`. AgentView reads them for SDK generation.
**Affected code**: `typescript-go/toolbox/extract.go`, `tool/tool.go` (new fields), `packaging/internal/source/source.go` (pass through new fields)

The checker already provides everything needed (proven by `signature_test.go`):
- `param.Name` — parameter name from symbol
- `ch.TypeToStringEx(paramType, ...)` — param type as TS string
- `ch.GetReturnTypeOfSignature(sig)` + `TypeToStringEx` — return type as TS string

**Extension** (~20 lines in extract.go):

```go
type ParamType struct {
    Name     string // e.g., "a"
    TypeText string // e.g., "number"
    Optional bool   // true if param has ?
}

type ToolMetadata struct {
    Description  string
    ParamsSchema Schema
    ParamTypes   []ParamType // NEW: ordered per-param TS type info
    ReturnType   string      // NEW: TS return type string (e.g., "string", "Promise<string>")
}
```

**Status**: Validated — `signature_test.go` proves the exact API calls work. The test at line 81-98 extracts `"channelID: string"`, `"payload: { message: {...} }"` etc. Return type available via `ch.GetReturnTypeOfSignature(sig)`.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `ToolMetadata.ParamTypes` | new field | `enrichToolMetadata()` → `PackageTool` → `ResolvedTool` → `AgentView` | Ordered param name + TS type string pairs |
| `ToolMetadata.ReturnType` | new field | same chain | TS return type string |
| `extractParamTypes(ch, sig)` | internal func | `ExtractToolMetadata()` | `[]ParamType` |

---

### Element: AgentView Type

**What**: The agent-visible API surface. Carries tool names (with bound resource levels stripped), visible param schemas, TS type info for SDK generation, descriptions, and metadata.
**Where**: New file `toolset/agentview.go`
**Wiring**: Produced by `ResolvedToolset.AgentView()`. Consumed by codemode (SDK generation) and service (MCP tool list).
**Affected code**: New file `toolset/agentview.go`

```go
type AgentView struct {
    Tools []AgentTool
}

type AgentTool struct {
    Name         string         // Shortened name (bound resource levels stripped)
    Description  string
    ParamsSchema map[string]any // JSON Schema — only visible params
    ParamTypes   []ParamType    // TS type info — only visible params, ordered
    ReturnType   string         // TS return type string
    ReadOnly     bool
    Idempotent   bool
}

// ParamType mirrors the typescript-go extraction output.
type ParamType struct {
    Name     string
    TypeText string
    Optional bool
}
```

**Key behavior**: When `Resolve()` applies bindings:
- Hidden params are removed from `ParamsSchema` and `ParamTypes`
- Bound resource levels are stripped from tool names (`account.tickets.get` -> `tickets.get`)
- Visible params keep their TS type info intact

**Status**: Validated — straightforward data mapping

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `ResolvedToolset.AgentView()` | method | Codemode, MCP service | `AgentView` with filtered tools |
| `AgentTool.ParamTypes` | field | Codemode SDK generation | Ordered visible param TS types |
| `AgentTool.ParamsSchema` | field | MCP tool listing, JSON Schema validation | Visible param JSON Schema |

---

### Element: Enhanced Resolve

**What**: `Builder.Resolve()` accepts `Config` and returns a `ResolvedToolset` that knows about hidden params, bound values, and compiled CEL expressions.
**Where**: `toolset/toolset.go` — modify existing `Resolve()` signature
**Wiring**: Reads loaded packages, applies resource-level bindings using the two-tier model (package canonical names -> toolset CEL bindings), compiles CEL expressions, generates AgentView.
**Affected code**: `toolset/toolset.go`, `toolset/toolset_test.go`, all callers of `Resolve()` (codemode tests, invoke tests, tooltest helpers)

**Signature change**: `Resolve()` -> `Resolve(cfg Config) (ResolvedToolset, error)`

**Empty Config gives backward compat**: Passing `Config{}` produces the same result as today — all tools visible, no bindings.

**Resolve flow**:
1. Collect all tools from loaded packages
2. For each tool, merge resource-level bindings (from Config.ResourceBindings matched by canonical name from PackageTool.ResourceParams) with per-tool bindings (from Config.Tools)
3. Compile all CEL value and check expressions via internal CEL engine
4. Pre-evaluate static bindings (literals like `"'#engineering'"`)
5. Build ResolvedToolset with compiled bindings attached
6. AgentView is derivable from the resolved state

**Status**: Validated — extends existing seam. Callers pass empty Config for current behavior.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `Builder.Resolve(cfg Config)` | method | All consumers | `(ResolvedToolset, error)` |
| Compiled CEL programs | internal state | `ValidateCall()` | Pre-compiled for call-time evaluation |
| Resource binding propagation | internal logic | All `account.*` tools | Inferred params bound by canonical name |

---

### Element: ValidateCall

**What**: At invoke time, evaluates bindings against agent-provided params + context. Returns full param set (hidden + visible, all resolved). Fails if check expression returns false.
**Where**: New file `toolset/validate.go`
**Wiring**: Called by `invoke.Run()` before dispatching to runtime. Replaces current direct tool lookup.
**Affected code**: New file `toolset/validate.go`, `invoke/invoke.go` (call ValidateCall before dispatch)

```go
func (r ResolvedToolset) ValidateCall(toolName string, agentParams map[string]any) (map[string]any, error)
```

**Flow**:
1. Look up BoundTool in resolved toolset by agent-visible name
2. For each binding: evaluate `value` CEL expression with `{params: agentParams, context: r.context}`
3. For each binding with `check`: evaluate guard. If false -> policy violation error
4. Merge: agent-provided visible params + resolved hidden params -> full param set
5. Return full params ready for runtime dispatch

**Status**: Validated — standard cel-go `Program.Eval()` pattern

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `ResolvedToolset.ValidateCall(name, params)` | method | `invoke.Run()` | `(map[string]any, error)` — full params or policy error |
| Policy violation error | error type | Caller | Clear error: "check failed: channel not in allowed_channels" |

---

### Element: Codemode Refactor

**What**: Refactor codemode to consume `AgentView` for SDK generation instead of reading raw `ResolvedToolset` tools directly. `preludeForTools()` and `typecheckSDKSource()` take `AgentView`.
**Where**: `codemode/codemode.go` — modify `Run()`, `preludeForTools()`, `typecheckSDKSource()`
**Wiring**: `Run()` calls `resolved.AgentView()` once. SDK generators use `AgentTool.ParamTypes` and `AgentTool.ReturnType` to emit typed declarations. Tool invocation still uses full `ResolvedToolset` via `invoke.Run()`.
**Affected code**: `codemode/codemode.go`, `codemode/codemode_test.go`

**Key change in `typecheckSDKSource()`**: Instead of importing tool modules for `Parameters<typeof toolmod>`:
```typescript
// Before (imports tool source):
import toolmod0 from "./tools/calc.add.ts";
add(args: Parameters<typeof toolmod0>[0]): ToolResult<ReturnType<typeof toolmod0>> { ... }

// After (uses pre-extracted types from AgentView):
add(args: { a: number; b: number }): string { return __invokeTool<string>("calc.add", args); }
```

This removes codemode's dependency on tool source filesystems for type generation. Codemode still needs tool source FS for the actual QuickJS execution (bundling + eval), but type checking uses AgentView types.

**Status**: Validated — straightforward refactor. Existing tests verify the same behavior.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `preludeForTools(view AgentView)` | internal func | QuickJS runtime | JS prelude with tool table |
| `typecheckSDKSource(view AgentView)` | internal func | TypeScript checker | TS declarations with typed methods |
| `Run(resolved, code)` | public func (unchanged sig) | Callers | Delegates to AgentView internally |

---

### Element: MCP Service Wiring

**What**: Service layer loads toolset, resolves with config, serves `AgentView` as MCP tool list.
**Where**: `service/service.go` — replace stubs with actual toolset integration
**Wiring**: `ExecuteToolDiscovery` builds a toolset, resolves with config, returns `AgentView.Tools` as MCP tool descriptors.
**Affected code**: `service/service.go`, `service/service_test.go`

**Status**: Validated — service is currently stubs. This fills them in with real toolset integration.

#### Code Affordances

| Affordance | Type | Wires To | Returns |
|------------|------|----------|---------|
| `ExecuteToolDiscovery(ctx, req)` | func | `toolset.Resolve(cfg)` -> `AgentView` | MCP tool list with visible params |
| `ExecuteToolAction(ctx, req)` | func | `ResolvedToolset.ValidateCall()` -> `invoke.Run()` | Tool execution result |

---

## Fit Check (R x Solution)

| | E1: Binding Types | E2: CEL Engine | E3: Resource Params | E4: TS Metadata | E5: AgentView | E6: Enhanced Resolve | E7: ValidateCall | E8: Codemode | E9: MCP Service |
|---|---|---|---|---|---|---|---|---|---|
| R0: Stateless API view | | | | | ✅ | ✅ | | | |
| R1: CEL binding engine | ✅ | ✅ | | | | ✅ | | | |
| R2: Hidden params | ✅ | | | | ✅ | ✅ | ✅ | | |
| R3: Check expressions | ✅ | ✅ | | | | | ✅ | | |
| R4: Two-tier resource binding | ✅ | | ✅ | | | ✅ | | | |
| R5: AgentView with TS types | | | | ✅ | ✅ | ✅ | | | |
| R6: Codemode consumes AgentView | | | | ✅ | ✅ | | | ✅ | |
| R7: MCP serves AgentView | | | | | ✅ | | | | ✅ |
| R8: ValidateCall | ✅ | ✅ | | | | | ✅ | | |

Every R row has at least one check. Every element column has at least one check. No gaps.

## Rabbit Holes

- **CEL dependency size**: cel-go pulls in protobuf and antlr. This adds ~5MB to the binary.
  - **Resolution: Accept.** CEL is the right expression language for this use case. The design doc already specifies CEL. The dependency cost is acceptable for a server-side tool.

- **Singularization for resource param inference**: `companies` -> `companie_id` (strip trailing 's' is naive).
  - **Resolution: Patch.** Use simple strip-s for now. Package authors can override with explicit `resource_bindings` in the manifest. This is already acknowledged in the tool definition spec as "ugly but functional." No need for a full inflection library.

- **typescript-go fork coordination**: Changes to `extract.go` need to be published to the fork before toolbox can consume them.
  - **Resolution: Accept.** The fork is at `/home/mackross/dev/vendor/typescript-go` and the replace directive can point to a local path during development. Publish to `github.com/mackross/typescript-go` when ready. This is the existing workflow.

- **Backward compatibility of Resolve() signature change**: All callers need to pass `Config{}`.
  - **Resolution: Accept.** There are few callers (codemode tests, invoke tests, tooltest helpers). Update them to pass `Config{}`. No external consumers yet.

- **AgentView ParamTypes after binding hides params**: If a hidden param has a TS type that references other visible params (e.g., in a union), removing it could produce invalid TS.
  - **Resolution: Patch.** Hidden params are removed from `ParamTypes` independently. Since tool params are a flat object type (each property is independent), removing one property doesn't affect others. If a tool has interdependent param types, the package author must handle that in the tool code, not the binding layer.

- **CEL namespace collision**: `params.channel` and `context.channel` — what if both exist?
  - **Resolution: By design.** CEL expressions explicitly qualify: `params.X` vs `context.X`. No ambiguity.

## No-Gos

- **No session management**: This is stateless resolve. Sessions are a future layer that consumes this.
- **No credential injection into tools**: Credentials are for the transport layer (mitmproxy). Tools never see secrets. This feature doesn't change that boundary.
- **No toolboxID or snapshot persistence**: That's the product/API layer above codemode, not toolset's concern.
- **No nested CEL binding paths**: Bindings operate on top-level params only. `params.payload.message` is not supported — bind `payload` as a whole or not at all. This matches the flat param model.
- **No dynamic tool selection**: The harness decides which tools are in the toolset. Bindings scope and filter params, not tool availability.
- **No full inflection library for singularization**: Strip-s + manifest override is the scope.

## Technical Validation

**Codebase reviewed**:
- `toolset/toolset.go` — Builder, ResolvedToolset, Resolve() (lines 1-81)
- `tool/tool.go` — Package, PackageTool, ResolvedTool types (lines 1-62)
- `packaging/internal/source/source.go` — LoadedPackage, ResolvedTools(), enrichToolMetadata()
- `packaging/internal/manifest/manifest.go` — DevManifest, Compile(), InferAccessMode, InferToolName
- `codemode/codemode.go` — Run(), preludeForTools(), typecheckSDKSource() (lines 1-202)
- `invoke/invoke.go` — Run(), RunWithVFS(), session caching (lines 1-172)
- `service/service.go` — Stub APIs (lines 1-69)
- `typescript-go/toolbox/extract.go` — ExtractToolMetadata, ToolMetadata, findDefaultExportFunction
- `typescript-go/toolbox/signature_test.go` — Proof that checker extracts param types and return types
- `docs/pkg-toolset-design.md` — Full binding model design (198 lines)
- `docs/codemode-overview.md` — Codemode spec including SDK generation (691 lines)
- `docs/pkg-tool-definition-spec.md` — Resource path naming and param inference rules

**Approach validated**: cel-go v0.27.0 confirmed available. typescript-go checker confirmed to extract per-param TS types and return types (proven by signature_test.go). All elements trace to actual code with specific file paths and line numbers.

**Flagged unknowns resolved**: All validated. No remaining unknowns.

**Test strategy**: TDD — start with tests that load a fixture package, resolve with bindings + context, and assert AgentView output. Then test ValidateCall with check expressions. Then test codemode SDK generation from AgentView. Existing test fixtures (calc, google-workspace) provide the foundation.

---

## Status: Shape Go — approved 2026-03-23
