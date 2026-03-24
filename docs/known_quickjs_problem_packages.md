# Known QuickJS Problem Packages

Toolbox runs JavaScript tools in QuickJS (compiled to WASM). Most npm packages work when bundled via esbuild with `PlatformBrowser` export conditions. However, packages that use **persistent timers** (`setInterval` heartbeats, long-lived `setTimeout` job queues) can hang because QuickJS's `js_std_loop` cannot resolve interleaved macrotask timers and microtask promise continuations in these patterns.

## What works

- `setTimeout(fn, 0)` and `setInterval` with `clearInterval` — transient timers resolve fine
- `.then()` chains around `fetch()` — even deep chains
- `Promise.resolve().then().then().then()` — microtask chains work
- Any package using transient `setTimeout` for retry backoff

## What hangs

Packages that keep timers alive as part of their normal request path — typically rate limiters and connection pool managers.

## Problem packages

### bottleneck

**Used by:** `octokit` (via `@octokit/plugin-throttling` and `@octokit/plugin-retry`)

**Root cause:** `yieldLoop()` creates `new Promise(resolve => setTimeout(resolve, 0))` to deliberately yield to the macrotask queue between scheduling steps. With three nested Bottleneck instances per octokit request (retry limiter, throttle limiter, throttle group), the interleaving of macrotask timers and microtask promise continuations creates a deadlock in QuickJS's event loop.

**Workaround:** Use `@octokit/request` directly (bypasses Bottleneck) or use raw `fetch` + `zod` validation. Alternatively, create a Bottleneck stub that executes `schedule()` immediately via `Promise.resolve().then(() => task(...args))`.

### p-queue

**Used by:** `@slack/web-api`

**Root cause:** Uses `setInterval` for rate-limit window management. The interval persists as long as tasks are queued.

**Workaround:** Use raw `fetch` to the Slack API with `p-retry` for retries.

### @apollo/client (with polling)

**Root cause:** `pollInterval` creates recurring `setTimeout` chains for data polling.

**Workaround:** Use `graphql-request` instead (zero timers, pure fetch).

## Safe packages

| Category | Safe | Notes |
|----------|------|-------|
| HTTP | `ky` | Built on `globalThis.fetch`, transient timers only |
| Validation | `zod` | Pure computation, no async |
| Retry | `p-retry`, `cockatiel`, `async-retry` | Transient `setTimeout` delays that drain naturally |
| GraphQL | `graphql-request`, `urql` (core) | Pure fetch wrappers |
| SaaS SDKs | `@notionhq/client`, `stripe` | Fetch-based, transient retry timers |
| SaaS SDKs | `@sendgrid/mail` | No retry/timer logic |
| GitHub | `@octokit/request` | Direct fetch, no Bottleneck |

## General rule

If a package uses `setInterval` or creates `setTimeout` callbacks that spawn further timers as part of a job queue or connection pool, it will likely hang in QuickJS. Packages that only use `setTimeout` for one-shot delays (retry backoff, request timeout) are safe.
