# Tool & Package Definition Spec

## Package Identity

A package is identified by a git URL and a semver version tag:

```
github.com/your-org/zendesk-tools@v1.2.0
github.com/acme-corp/internal-tools@v0.3.1
gitlab.com/tools/slack@v2.0.0
```

The format is `git-host/org/repo@version`.

Any git host works. No dependency on GitHub-specific features (releases, artifacts). The source at the tagged commit IS the package.

## Versioning

Semver with strict rules:

- **Major** — breaking changes: tool removed, param removed, param renamed, param type changed, resource path changed, behavior change that existing bindings would break on
- **Minor** — additive only: new tools added, new optional params on existing tools. Existing toolset bindings work unchanged.
- **Patch** — bugfixes only. Same tools, same params, same behavior contract. A tool was doing something wrong, now it does it right.

Versions are pinned in the toolset. There is no automatic upgrading.

## Package Structure

```
my-tools/
├── toolbox.pkg.json        # authored source package definition
├── tools/                   # tool source files (default location)
│   ├── users.list.ts
│   ├── users.get.ts
│   ├── users.calendars.events.list.ts
│   └── channels.messages.send.ts
└── assets/                  # WASM binaries (default location)
    └── gwc.wasm
```

Convention over configuration. The source package definition can omit anything that follows defaults.

Built artifacts may also include:

```
dist/
└── toolbox.pkg.compiled.json
```

`toolbox.pkg.compiled.json` is the normalized compiled package form. In source mode, packaging compiles this form in memory by default.

Loading uses the same split:
- source package loading reads `toolbox.pkg.json`, compiles the normalized package form in memory, then validates that compiled form
- built package loading reads `toolbox.pkg.compiled.json` directly, then validates that compiled form

## Source Package Definition

The authored source package file is `toolbox.pkg.json`. It is thin. It declares package identity, tool entries, and package-level settings. Packaging then compiles that source form plus tool-source metadata into a normalized package model.

### Minimal source package:

```json
{
  "name": "slack",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/channels.messages.send.ts"
    }
  ]
}
```

Per-tool descriptions and some defaults may come from tool source during local development. Packaging compiles them into the normalized package form.

### Source package with explicit tool metadata:

```json
{
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "idempotent": true,
      "effect": "readOnly"
    }
  ]
}
```

Tools may optionally set `max_fetch_response_bytes` in their manifest entry to
override the default `fetch()` response-body limit for that tool. If omitted,
Toolbox uses a default limit of 10 MiB.

```json
{
  "name": "large-export-tool",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "entry_ts": "tools/export.download.ts",
      "idempotent": true,
      "effect": "readOnly",
      "max_fetch_response_bytes": 25000000
    }
  ]
}
```

## Tool Definitions

Each tool is a TypeScript file in the `tools/` directory. The filename is the resource path + method.

During packaging, Toolbox validates the exported public contract for each tool and emits JSON Schema artifacts from that contract. This happens at package build/validation time, not at invocation time.

Toolbox uses two validation modes for `toolbox.pkg.json`:
- dev validation accepts an incomplete manifest shape for local iteration and reports stricter distribution-only requirements as warnings
- distribution validation is strict and rejects missing required packaging metadata such as per-tool safety flags

The validation target is the compiled package form, not the raw source file. In source mode, packaging compiles `toolbox.pkg.json` plus tool-source metadata into a normalized package model in memory, then validates that compiled model against the dev or publishable schema. Built packages ship the compiled form directly as `toolbox.pkg.compiled.json`.

### Filename Convention

The filename defines the tool's resource path:

```
users.list.ts                       → method "list" on resource "users"
users.get.ts                        → method "get" on resource "users"  
users.calendars.events.list.ts      → method "list" on resource "events" under "calendars" under "users"
channels.messages.send.ts           → method "send" on resource "messages" under "channels"
clone.ts                            → method "clone", no resource (flat action)
```

Rules:
- Last segment = method (verb)
- Everything before = resource path (plural nouns)
- Each resource segment implies a `{singular}_id` param: `users` → `user_id`, `channels` → `channel_id`
- `list` method on a resource does not require that resource's ID (operates on collection)
- Flat actions (no dots before the verb) have no inferred resource params

### Tool File Format

A tool file exports `params`, `metadata`, and an `execute` function:

```typescript
// tools/users.calendars.events.list.ts
//
// Resource path: users.calendars.events
// Inferred params: user_id, calendar_id (events.list doesn't need event_id)
// Declared params below are the method-specific params only.

export const params = {
  time_min: { type: "string", description: "Start of time range (RFC3339)" },
  time_max: { type: "string", description: "End of time range (RFC3339)" },
  max_results: { type: "number", description: "Max events to return", default: 50 }
}

export const metadata = {
  readOnly: true,
  idempotent: true,
  description: "List calendar events for a user"
}

export async function execute(params, ctx) {
  const result = await exec("gwc", [
    "calendar", "events", "list",
    "--user", params.user_id,
    "--calendar-id", params.calendar_id,
    "--time-min", params.time_min,
    "--time-max", params.time_max,
    "--max-results", String(params.max_results),
    "--format", "json"
  ]);
  return JSON.parse(result.stdout);
}
```

Note: `params.user_id` and `params.calendar_id` are available in `execute` because they're inferred from the resource path. They don't need to be declared in `params` — they're always present.

Tool metadata is not only for static safety labeling. Fields such as `readOnly`, `idempotent`, and `effect` are also expected to inform recovery guidance later, for example helping an LLM decide whether to retry, re-read state, or choose a safer follow-up tool after a failed call.

### Pure TS Tool (no WASM asset)

```typescript
// tools/channels.messages.send.ts

export const params = {
  message: { type: "string", required: true, description: "Message text" }
}

export const metadata = {
  destructive: true,
  idempotent: false,
  description: "Send a message to a channel"
}

export async function execute(params, ctx) {
  const resp = await fetch("https://slack.com/api/chat.postMessage", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      channel: params.channel_id,
      text: params.message
    })
  });
  return await resp.json();
}
```

### Flat Action Tool (no resource hierarchy)

```typescript
// tools/clone.ts

export const params = {
  repo_url: { type: "string", required: true, description: "Git repository URL" },
  branch: { type: "string", default: "main", description: "Branch to clone" },
  depth: { type: "number", default: 1, description: "Clone depth" }
}

export const metadata = {
  readOnly: false,
  idempotent: true,
  description: "Clone a git repository"
}

export async function execute(params, ctx) {
  const result = await exec("git.wasm", [
    "clone",
    "--branch", params.branch,
    "--depth", String(params.depth),
    params.repo_url
  ]);
  return { success: result.exitCode === 0, output: result.stdout };
}
```

## Host Imports

Available to all tool execute functions:

### `fetch(url, opts)`

Makes an HTTP request. Routed through Go's proxy for credential replacement, scope enforcement, and audit logging.

- `url` — the target URL
- `opts` — standard fetch options: method, headers, body
- Returns: response object with status, headers, body
- Credentials are replaced automatically by the proxy — tool code never sees real secrets
- Response bodies are limited to 10 MiB by default; if the body exceeds the
  limit, `fetch()` fails with an explicit error instead of returning truncated
  partial data
- A tool can raise or lower that limit with `max_fetch_response_bytes` on its
  manifest entry

### `exec(binary, args)`

Executes a WASM binary asset in a WASIX sandbox.

- `binary` — name of the asset (as declared in manifest, e.g. `"gwc"`)
- `args` — command-line arguments
- Returns: `{ stdout: string, stderr: string, exitCode: number }`
- The WASM binary runs with HTTPS_PROXY configured — HTTP calls go through Go's proxy
- The binary has access to env vars and scoped filesystem from the toolset's env
- Compiled WASM modules are cached (LRU, sized by total bytes, configurable limit)

### `mcp(server, method, params)`

Calls a method on an MCP server managed by the runtime.

- `server` — MCP server identifier
- `method` — MCP method name
- `params` — method parameters
- Returns: MCP response

### `log(level, message)`

Structured logging, collected in the audit trail.

- `level` — "debug", "info", "warn", "error"
- `message` — log message

## Resource Path Conventions

Resource paths in tool filenames follow REST conventions:

- Resources are **plural nouns**: `users`, `tickets`, `channels`, `events`
- Methods are **verbs**: `get`, `list`, `create`, `update`, `delete`, `send`, `add`
- Last segment is always the method
- Each resource segment infers a `{singular}_id` parameter
- Singularization is simple: strip trailing `s` (handles most cases; `companies` → `companie_id` is ugly but functional; can add a singularization override in manifest later if needed)

### How resource params work

For `users.calendars.events.list.ts`:
- Resource path: `users.calendars.events`
- Inferred params: `user_id`, `calendar_id`
- `event_id` is NOT inferred because `list` operates on the collection
- The tool author declares only method-specific params (e.g. `time_min`, `time_max`)

For `users.calendars.events.get.ts`:
- Resource path: `users.calendars.events`
- Inferred params: `user_id`, `calendar_id`, `event_id`
- `get` operates on a specific resource, so the deepest ID is required

### Which methods require the deepest resource ID

Methods that operate on a specific resource instance:
- `get`, `update`, `delete` — require the deepest resource ID

Methods that operate on the collection:
- `list`, `create` — do NOT require the deepest resource ID

Custom verbs: the tool author can override by explicitly declaring the deepest ID in their `params` if they need it, or omitting it if they don't.

## Distribution

### TS source

TS files are the distribution format. The client compiles them locally using QuickJS on first use. The compiled bytecode is cached.

### WASM assets

WASM binaries are pre-built and committed to the repo (or attached to the git tag). They are fetched once and cached locally.

The WASM module cache uses LRU eviction sized by total bytes. Default limit is configurable by the operator. Pre-compiled (JIT'd) versions of WASM modules are cached for fast subsequent loads.

### Fetch flow

1. Toolset references `github.com/your-org/tools@v1.2.0`
2. Client checks local cache for this version
3. If not cached: clone/fetch the repo at tag `v1.2.0`
4. Read `manifest.json`
5. Compile TS tools to QuickJS bytecode, cache
6. Cache WASM assets, pre-compile with wasmer, cache compiled modules
7. Ready to execute

## What This Spec Does NOT Cover

- Toolset assembly and binding — see `docs/pkg-toolset-design.md`
- Single-tool execution semantics — see `invoke/README.md`
- Code mode SDK generation — see `docs/codemode-overview.md`
- MCP/HTTP/CLI service surface — see `service/README.md`
- Registry loading, caching, and discovery — see `registry/README.md`
