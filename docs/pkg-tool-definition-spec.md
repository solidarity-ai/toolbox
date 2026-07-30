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

### Source package with credentials:

```json
{
  "name": "github",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "instructions": "Create a personal access token in GitHub settings.",
      "inject": {
        "hosts": ["api.github.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    {
      "entry_ts": "tools/issues.list.ts",
      "idempotent": false,
      "effect": "readOnly"
    }
  ]
}
```

`credentials[].instructions` is optional. When present, Toolbox shows it to the
user during credential setup and in missing-credential status output so the
user knows where to get or create the required credential.

If a credential uses `inject.path_prefix`, `/` is the catch-all prefix and
matches both the bare host URL (for example `https://api.example.com`) and any
subpath on that host.

### Source package with explicit network allowlist:

```json
{
  "name": "webhook-relay",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.example.com", "*.internal.example.com"],
  "tools": [
    {
      "entry_ts": "tools/events.forward.ts",
      "idempotent": false,
      "effect": "readOnly"
    }
  ]
}
```

`allowed_hosts` is package-scoped and applies to every tool in the package.

- If `allowed_hosts` is omitted or empty, outbound network access is denied by default.
- Use `["*"]` to explicitly allow requests to any host.
- Exact hosts and subdomain wildcards such as `*.googleapis.com` are supported.

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

### Parameter & Return Value Types

Tool contracts are TypeScript types and support two tiers:

- **Native (preferred)**: `Date`, `Map`, `Set`, `RegExp`, `bigint`,
  `ArrayBuffer`, and the typed arrays (`Uint8Array`, `Uint8ClampedArray`,
  `Int8Array`, `Int16Array`, `Uint16Array`, `Int32Array`, `Uint32Array`,
  `Float32Array`, `Float64Array`, `BigInt64Array`, `BigUint64Array`), in
  addition to everything in the JSON tier. These cross the host boundary via
  the wire format and arrive as real objects in both directions:
  `input.when instanceof Date` holds inside the tool, and a returned `Map`
  is a real `Map` in the calling cell. Prefer the natural native type
  (`when: Date`, not `when: string` holding RFC3339) — codemode is the
  primary invocation surface and preserves it end-to-end.
- **JSON-compatible**: `string`, `number`, `boolean`, `null`, plain objects,
  arrays, and tuples of these. Tools whose params and return type stay within
  this tier are additionally JSON-callable: they can be invoked directly over
  JSON transports (MCP tool calls, SDK bridge direct invoke). **Direct
  MCP/JSON invocation is deprecated** and will be removed; do not constrain a
  contract to this tier for its benefit. Using a native type anywhere in the
  contract (including nested fields) makes the tool codemode-only — it is
  omitted from direct JSON tool listings and rejected by direct invocation
  (`PreparedTool.JSONCallable()` reports false and `JSONCallWhyNot()` names
  the offending path, e.g. `params.when: uses Date`).

Not representable in tool contracts (no wire model): `Error`, `DataView`,
`WeakMap`, `WeakSet`, `Promise`, `Symbol`, and function types. `any`,
`unknown`, and open object types (`Record<string, unknown>`-style index
signatures without a concrete value type) also make a tool non-JSON-callable.

### Filename Convention

The filename defines the tool's dotted method path:

```
users.list.ts                       → method "list" on resource "users"
users.get.ts                        → method "get" on resource "users"  
users.calendars.events.list.ts      → method "list" on resource "events" under "calendars" under "users"
channels.messages.send.ts           → method "send" on resource "messages" under "channels"
clone.ts                            → method "clone", no resource (flat action)
```

The last segment is the method. Earlier segments are ordinary namespaces unless
the package manifest declares an exact path as a resource selector. Nothing is
inferred from plurality or from verbs such as `list` and `get`.

Package resources are declared once:

```json
{
  "resources": [
    {"path":"users","params":[{"name":"user","binding_name":"user"}]},
    {"path":"users.calendars","params":[{"name":"calendar","binding_name":"calendar"}]},
    {"path":"users.calendars.events","params":[{"name":"start"},{"name":"end"}]}
  ]
}
```

Selector parameter groups are ordered and must form the beginning of every
matching tool signature. For a chain `users(user).calendars(calendar)`, the
signature must begin `(user, calendar, ...)`; reordering or interleaving method
parameters is invalid. A signature containing all of a node's parameters is
installed on the selected-resource surface; containing none installs it on the
callable collection surface. Partial groups are invalid.
This supports both `users(user).calendars.list()` and
`users(user).calendars(calendar).get()`, plus multi-argument selectors such as
`events(start, end)`. Resource selectors only capture parameters and return a
member API object; they never invoke tools themselves. Collection methods such
as `calendars.list()` remain properties of the callable selector and do not
conflict with its call signature. When a collection member deliberately uses a
function intrinsic name such as `name`, the tool keeps the natural name and the
displaced intrinsic is exposed as an underscore method such as `_name()`.
JavaScript plumbing names that are unlikely to be useful domain APIs are
reserved across generated resource API surfaces. These include `then`, `__proto__`,
`toString`, `valueOf`, the object ownership/prototype inspection methods, and
the legacy getter/setter hooks. Plausible domain vocabulary such as `name`,
`length`, `prototype`, `call`, `apply`, and `bind` remains available with an
underscore escape method for the displaced function behavior.

### Tool File Format

A tool file exports `params`, `metadata`, and an `execute` function:

```typescript
// tools/users.calendars.events.list.ts
//
// The package resource tree declares users(user) and calendars(calendar).

/** @effect readOnly */
export default async function tool(
  user: string,
  calendar: string,
  timeMin: string,
  timeMax: string,
): Promise<Event[]> {
  return fetchEvents({ user, calendar, timeMin, timeMax });
}
```

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
- Outbound network access is denied unless the package `allowed_hosts` permits
  the target host; `["*"]` is the explicit allow-all form
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

Resource paths are package-level API declarations rather than filename
heuristics. Each resource has a dotted path and one or more selector parameters.
Parameter names are exact TypeScript parameter names, and types must agree in
every consuming tool. Selector parameters are required and unique along a
resource chain. Ordinary filename segments that are not declared resources
remain namespaces; for example, with only `workbook` declared as a resource,
`workbook.officejs.run.ts` becomes `workbook(path).officejs.run(code)`.

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
