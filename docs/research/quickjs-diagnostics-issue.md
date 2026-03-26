# GitHub Issue: feat: developer diagnostics for QuickJS tool debugging

> Create with: `gh issue create --title "feat: developer diagnostics for QuickJS tool debugging" -F docs/research/quickjs-diagnostics-issue.md`
> Then delete this file.

## Context

When building tools that use npm dependencies, runtime errors in QuickJS are opaque:

```
TypeError: Cannot convert undefined or null to object
    at <anonymous> (__toolbox_run.js:5794:1)
```

Line 5794 of a bundled file with no source mapping. Tool authors (human or agent) can't diagnose what's missing.

## Proposed features

### 1. Source maps in error messages

esbuild can emit inline sourcemaps. Post-process QuickJS errors in Go: parse the sourcemap, map `__toolbox_run.js:5794:1` back to the original file and line, and return the mapped stack trace.

This is the highest-leverage change — every error becomes actionable.

### 2. Diagnose mode (pre-flight check)

Before running a tool for real, do a dry-run that:
- Bundles with esbuild (catches import resolution errors)
- Scans the bundle for references to known-missing globals (`navigator`, `crypto.subtle`, `btoa`, `setTimeout`, etc.)
- Reports which `node:*` externals are referenced (these will fail at runtime)
- Runs the bundle in QuickJS with a short timeout and returns the first error with a mapped stack trace
- Outputs a structured report: "missing globals: X, Y, Z; external node modules: A, B"

### 3. Runtime globals audit via Proxy

Inject a Proxy on `globalThis` before tool code runs that logs every property access returning `undefined`:

```
[audit] globalThis.crypto.subtle accessed -> undefined (needs polyfill)
[audit] globalThis.navigator.userAgent accessed -> undefined (needs shim)
```

This turns silent failures into explicit feedback.

## Current state

- `Host.Console` callback is now available (PR #30) — tool console output routes to Go
- esbuild bundles npm deps from `node_modules` in dev mode
- Browser shims (process, btoa/atob, TextEncoder, crypto stub) are needed per-tool via `shims.ts`

## Known issue: QuickJS promise chain hang

npm SDKs like `octokit` that wrap `fetch()` in multiple `.then()` chains hang indefinitely in QuickJS. Direct `await fetch()` works fine. The `js_std_loop` in the C layer runs but doesn't resolve deeply chained promises from within `SetAsyncFunc` callbacks.

Debug output showing the hang point:
```
[log] SHIMS: all loaded
[log] STEP: octokit imported
[log] STEP: octokit created
[log] STEP: octokit.rest.issues.get() called
<hangs here — promise never resolves>
```

### Root cause identified

The `Octokit` class (from the `octokit` package) wraps all requests through **Bottleneck**, a rate limiter that uses `setInterval`/`setTimeout` for job scheduling. While `setTimeout` and `setInterval` work individually in QuickJS, Bottleneck's specific pattern of timer-managed job queues causes `js_std_loop` to hang — likely because Bottleneck keeps timers alive that prevent the event loop from settling.

Key findings from debugging:
- `setTimeout(fn, 0)` works
- `setInterval` with `clearInterval` works
- `setTimeout` wrapping `fetch()` works
- `.then()` chains around `fetch()` work (even 3+ levels deep)
- `@octokit/request` (direct, no Bottleneck) **works**
- `new Octokit().request()` (goes through Bottleneck) **hangs**

### Workaround

Use `@octokit/request` directly instead of the `Octokit` class:
```typescript
import { request } from "@octokit/request";
const { data } = await request("GET /repos/{owner}/{repo}/issues/{issue_number}", {
  owner, repo, issue_number, headers: { authorization: "token " + token },
});
```

### Potential fixes for full SDK compatibility

1. **Investigate Bottleneck's timer pattern** — identify the specific timer lifecycle that prevents `js_std_loop` from completing
2. **Bundle octokit without Bottleneck** — esbuild could alias the Bottleneck import with a pass-through stub
3. **Fix QuickJS event loop handling** in `fastschema/qjs` for long-lived timer patterns
