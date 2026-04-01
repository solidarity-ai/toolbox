# Tool Package Credential Manifest Spec

This document describes the credential-related manifest surface that actually shipped in M001.

It is for **package authors** writing `toolbox.devpkg.json` / compiled package metadata who need to know:
- what can go in `credentials`
- how tool-level overrides work
- how `provider`, `scopes`, `inject`, and `allowed_hosts` interact
- which edge cases fail closed

This spec is derived from the delivered M001 implementation and the slice summaries for S01-S12, not just the RFC.

---

## 1. What credentials are for

Credentials declare **transport-managed auth**.

That means:
- the package declares what auth it needs
- toolbox resolves and injects auth at runtime
- tool code does **not** receive tokens, API keys, or credential-shaped params
- secrets live in the local secret store, not in the manifest

If your tool is supposed to call a protected API, declare that auth in the manifest and write the tool as if `fetch()` is already authenticated.

Example:

```json
{
  "module": "github.com/example/github-issues",
  "name": "github-issues",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.github.com"],
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "inject": {
        "hosts": ["api.github.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/github-issues.get.ts" }
  ]
}
```

---

## 2. Package-level rules

### `module` is required when credentials are declared

If the package declares any credentials, it must have a valid fully qualified `module`.

Why:
- the module becomes the root secret namespace
- credentials are looked up under that package namespace
- package isolation depends on it

Example:

```json
{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "credentials": [ ... ]
}
```

Without credentials, local packages may omit `module`. Once credentials are present, they must not.

### Secrets never belong in the manifest

The manifest may declare:
- credential name
- credential type
- provider
- scopes
- inject hosts/method

The manifest must **not** contain:
- access tokens
- refresh tokens
- client secrets
- API keys
- bearer tokens
- passwords

The parser rejects secret-valued declaration fields.

---

## 3. Package-level `credentials`

Top-level shape:

```json
{
  "credentials": [
    {
      "name": "workspace",
      "type": "oauth2",
      "provider": "google",
      "scopes": [
        "openid",
        "email",
        "https://www.googleapis.com/auth/admin.directory.user.readonly"
      ],
      "inject": {
        "hosts": ["admin.googleapis.com"],
        "method": "bearer_header"
      }
    }
  ]
}
```

Each credential object is declarative metadata used to build runtime transport rules.

### Fields

#### `name`
Stable credential identifier inside the package.

Examples:
- `github_token`
- `workspace`
- `slack_workspace`

This name is also part of the runtime secret namespace.

Examples:
- simple bearer credential secret key:
  - `<module>/<credential-name>`
- OAuth durable family:
  - `<module>/<credential-name>/client_id`
  - `<module>/<credential-name>/client_secret`
  - `<module>/<credential-name>/refresh_token`
  - runtime access token family member:
    - `<module>/<credential-name>/access_token`

#### `type`
Supported shipped types:
- `oauth2`
- `bearer`
- `api_key`

Runtime injection methods shipped in M001 cover:
- bearer header
- basic auth
- API key in header
- API key in query string

Notes:
- `oauth2` uses provider metadata plus refresh lifecycle
- `bearer` resolves one secret value and attaches it to matching requests
- `api_key` uses one secret value and injects it according to `inject.method`

#### `provider`
Only valid for `type: "oauth2"`.

Two supported shapes:

**Built-in provider name**

```json
"provider": "google"
```

Shipped built-ins are intentionally limited. M001 proved built-ins for:
- `google`
- `slack`
- `microsoft`

**Explicit provider endpoints**

```json
"provider": {
  "auth_url": "https://auth.custom.com/oauth/authorize",
  "token_url": "https://auth.custom.com/oauth/token"
}
```

Rules:
- required for `oauth2`
- rejected for non-`oauth2`
- built-in provider names must be known
- explicit endpoint URLs must be valid HTTPS URLs
- the explicit provider object survives compile/load round-trips

#### `scopes`
Only meaningful for `oauth2`.

Example:

```json
"scopes": [
  "openid",
  "email",
  "https://www.googleapis.com/auth/gmail.readonly"
]
```

Rules:
- required for `oauth2`
- must not be empty for `oauth2`
- auth bootstrap uses them to build the authorize URL
- runtime token refresh assumes the durable auth state was authorized with the declared scopes

#### `inject`
Declares where and how the credential is attached.

Shape:

```json
"inject": {
  "hosts": ["api.github.com"],
  "method": "bearer_header"
}
```

---

## 4. `inject` semantics

### `inject.hosts`
List of hosts the credential may be attached to.

Supported matching semantics:
- exact host
- wildcard subdomain host
- optional path-prefix matching in the runtime rule model

Examples:

```json
"hosts": ["api.github.com"]
```

```json
"hosts": ["*.googleapis.com"]
```

Important edge case:
- `*.googleapis.com` does **not** match `googleapis.com`
- wildcard requires at least one subdomain segment

Rules are canonicalized and checked for ambiguity during resolution.
Ambiguous overlapping rules are rejected before runtime.

### `inject.method`
Supported shipped values:
- `bearer_header`
- `basic_auth`
- `api_key_header`
- `api_key_query`

#### `bearer_header`
Attaches:

```http
Authorization: Bearer <secret-or-access-token>
```

Typical for:
- OAuth2 access tokens
- plain bearer tokens

#### `basic_auth`
Attaches HTTP Basic auth.

This is transport-owned and hidden from the tool, same as the other methods.

#### `api_key_header`
Requires `inject.headerName`.

Example:

```json
"inject": {
  "hosts": ["api.example.com"],
  "method": "api_key_header",
  "headerName": "X-API-Key"
}
```

Rules:
- `headerName` must be present
- `headerName` must be a valid HTTP header name
- `queryName` must not be present

#### `api_key_query`
Requires `inject.queryName`.

Example:

```json
"inject": {
  "hosts": ["api.example.com"],
  "method": "api_key_query",
  "queryName": "api_key"
}
```

Rules:
- `queryName` must be present
- `queryName` must be a valid query parameter name
- `headerName` must not be present

### Stray method-specific fields are rejected

Examples that fail validation:
- `bearer_header` with `headerName`
- `bearer_header` with `queryName`
- `api_key_header` without `headerName`
- `api_key_query` without `queryName`

This is intentional. The manifest fails closed rather than leaving runtime behavior ambiguous.

---

## 5. `allowed_hosts` is separate from credentials

Credential matching and network egress are distinct.

- `credentials[*].inject.hosts` answers:
  - where auth may be attached
- `allowed_hosts` answers:
  - where the tool may make outbound requests at all

You usually want both to agree.

Example:

```json
{
  "allowed_hosts": ["admin.googleapis.com"],
  "credentials": [
    {
      "name": "workspace",
      "type": "oauth2",
      "provider": "google",
      "scopes": [
        "openid",
        "email",
        "https://www.googleapis.com/auth/admin.directory.user.readonly"
      ],
      "inject": {
        "hosts": ["admin.googleapis.com"],
        "method": "bearer_header"
      }
    }
  ]
}
```

### Deny-by-default
If no effective `allowed_hosts` are declared for a fetch-capable tool, runtime network access is denied by default.

That means:
- auth metadata can still resolve
- but the request is blocked before network I/O

### Tool-level allowlist overrides
Tools can refine network policy with:
- `allowed_hosts`
- `allowed_hosts_extend`

Rules:
- tool `allowed_hosts` replaces package `allowed_hosts`
- tool `allowed_hosts_extend` augments inherited package allowlist
- declaring both on the same tool is invalid

---

## 6. Tool-level `credentials` overrides

Each tool may also declare its own `credentials` field.

This field has **presence semantics**, not just value semantics.

### Case A: field absent → inherit package credentials

```json
{
  "entry_ts": "tools/users.list.ts"
}
```

This tool inherits package-level credentials.

### Case B: explicit empty array → no credentials

```json
{
  "entry_ts": "tools/status.check.ts",
  "credentials": []
}
```

This means:
- do not inherit package credentials
- run this tool unauthenticated

### Case C: explicit non-empty array → replace package credentials

```json
{
  "entry_ts": "tools/calendar.events.list.ts",
  "credentials": [
    {
      "name": "google_calendar",
      "type": "oauth2",
      "provider": "google",
      "scopes": [
        "https://www.googleapis.com/auth/calendar.readonly"
      ],
      "inject": {
        "hosts": ["www.googleapis.com"],
        "method": "bearer_header"
      }
    }
  ]
}
```

This means:
- do not merge with package credentials
- use only this replacement credential set

This replace-not-merge behavior was proven across:
- source resolution
- toolset policy assembly
- invoke/goFetch
- MITM parity

### Guidance for authors

Use tool-level credentials only when one tool truly needs:
- a different provider
- different scopes
- a different inject host set
- or explicit unauthenticated behavior

Otherwise, keep credentials at the package level.

---

## 7. Secret namespace behavior

Authors do **not** put secret keys in the manifest, but you should understand how they are derived.

### Package-scoped default

For a package:

```json
"module": "github.com/example/google-workspace"
```

and credential:

```json
"name": "workspace"
```

runtime secret roots are package-scoped.

Examples:
- bearer/API-key single-secret key:
  - `github.com/example/google-workspace/workspace`
- OAuth durable family members:
  - `github.com/example/google-workspace/workspace/client_id`
  - `github.com/example/google-workspace/workspace/client_secret`
  - `github.com/example/google-workspace/workspace/refresh_token`
- runtime OAuth access token family member:
  - `github.com/example/google-workspace/workspace/access_token`

### Tenant-scoped storage exists

Auth state may also be stored under:

```text
<module>/tenant/<tenant>/<credential>/...
```

Example:

```text
github.com/example/google-workspace/tenant/acme/workspace/refresh_token
```

Important shipped limitation:
- tenant-scoped **storage** exists
- tenant-scoped **runtime selection** is not fully productized through normal invoke/MCP flows yet

So package authors may rely on namespace shape existing, but should not claim end-to-end tenant selection unless the consuming harness explicitly supports it.

### Package isolation

Two packages using the same credential name do not share secrets by default.

Example:
- `github.com/example/google-workspace/workspace/...`
- `github.com/example/google-calendar/workspace/...`

These are isolated because module identity is the namespace root.

---

## 8. OAuth-specific author guidance

### Recommended built-in-provider manifest

```json
{
  "module": "github.com/example/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["admin.googleapis.com"],
  "credentials": [
    {
      "name": "workspace",
      "type": "oauth2",
      "provider": "google",
      "scopes": [
        "openid",
        "email",
        "https://www.googleapis.com/auth/admin.directory.user.readonly"
      ],
      "inject": {
        "hosts": ["admin.googleapis.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "idempotent": true,
      "effect": "readOnly"
    }
  ]
}
```

### Recommended explicit-provider manifest

```json
{
  "module": "github.com/example/custom-oauth",
  "name": "custom-oauth",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.custom.com"],
  "credentials": [
    {
      "name": "custom_service",
      "type": "oauth2",
      "provider": {
        "auth_url": "https://auth.custom.com/oauth/authorize",
        "token_url": "https://auth.custom.com/oauth/token"
      },
      "scopes": ["read"],
      "inject": {
        "hosts": ["api.custom.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    {
      "entry_ts": "tools/things.list.ts",
      "idempotent": true,
      "effect": "readOnly"
    }
  ]
}
```

### What `provider` means

`provider` is only for the OAuth control plane:
- authorization URL
- token exchange URL
- refresh URL behavior

It does **not** control:
- API host selection
- scope selection
- token injection target hosts

Those come from:
- `scopes`
- `inject.hosts`
- tool code itself

---

## 9. Simple credential author guidance

### Bearer token example

```json
{
  "module": "github.com/example/github-issues",
  "name": "github-issues",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.github.com"],
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "inject": {
        "hosts": ["api.github.com"],
        "method": "bearer_header"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/github-issues.get.ts" }
  ]
}
```

### API key in header example

```json
{
  "module": "github.com/example/weather",
  "name": "weather",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.weather.example.com"],
  "credentials": [
    {
      "name": "weather_api_key",
      "type": "api_key",
      "inject": {
        "hosts": ["api.weather.example.com"],
        "method": "api_key_header",
        "headerName": "X-API-Key"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/forecast.get.ts" }
  ]
}
```

### API key in query string example

```json
{
  "module": "github.com/example/maps",
  "name": "maps",
  "runtime": "typescript-sandbox",
  "allowed_hosts": ["api.maps.example.com"],
  "credentials": [
    {
      "name": "maps_api_key",
      "type": "api_key",
      "inject": {
        "hosts": ["api.maps.example.com"],
        "method": "api_key_query",
        "queryName": "key"
      }
    }
  ],
  "tools": [
    { "entry_ts": "tools/routes.get.ts" }
  ]
}
```

---

## 10. Error cases authors should expect

These fail validation or resolution intentionally.

### Missing module when credentials are declared

Invalid:

```json
{
  "name": "bad-package",
  "runtime": "typescript-sandbox",
  "credentials": [ ... ]
}
```

### Unknown OAuth provider name

Invalid:

```json
"provider": "not-a-provider"
```

### Explicit provider object on non-oauth2 credential

Invalid.

### Non-HTTPS explicit OAuth endpoints

Invalid:

```json
"provider": {
  "auth_url": "http://auth.custom.com/oauth/authorize",
  "token_url": "https://auth.custom.com/oauth/token"
}
```

### Empty OAuth scopes

Invalid.

### Invalid inject method metadata

Invalid examples:
- `api_key_header` without `headerName`
- `api_key_query` without `queryName`
- `bearer_header` with `headerName`
- `bearer_header` with `queryName`

### Duplicate credential names in one declaration list

Credential names must be unique within the same list.

### Ambiguous overlapping inject rules

If runtime host/path rules still overlap ambiguously after specificity rules, resolution fails instead of guessing.

### `allowed_hosts` and `allowed_hosts_extend` on the same tool

Invalid.

### Tool-visible auth params

Do not add token-shaped tool params to work around auth. M001 explicitly moved auth ownership to transport/runtime.

---

## 11. Authoring recommendations

### Prefer package-level credentials unless you need a real override

Good default:
- one credential at package level
- tools inherit it

Use tool-level credentials only when one tool needs:
- different scopes
- a different provider
- different inject hosts
- or explicit unauthenticated behavior

### Keep `allowed_hosts` tight and explicit

Declare only the hosts the package really needs.
If tests or local harnesses rewrite hosts, update:
- tool URL
- `inject.hosts`
- `allowed_hosts`

together.

### Match API hosts truthfully

If your tool calls:
- `admin.googleapis.com`

then declare that host, not a broad guess, unless you truly need the wildcard.

### Treat reference packages as truthful examples

The shipped examples are:
- `testutil/fixtures/toolbox.pkgs/github-issues` — simple bearer token hidden from the tool
- `testutil/fixtures/toolbox.pkgs/google-workspace` — OAuth2 package with built-in provider, scopes, allowlist, and bearer-header injection

---

## 12. Current limitations package authors should know

These are shipped limitations, not authoring mistakes.

- Built-in OAuth provider coverage is intentionally small; explicit endpoints are the fallback.
- Tenant-scoped durable auth storage exists, but tenant-aware runtime selection is not fully surfaced through normal invocation flows yet.
- Audit events are emitted from the shared transport seam; auth bootstrap itself does not currently emit the same transport `credential_*` events unless a higher-level harness adds its own reporting.
- For proxy-backed WASIP2 guest flows, the known TinyGo `Netdev not set` runtime limitation can still block end-to-end proof even when manifest auth metadata is correct.

---

## 13. Minimal checklist for package authors

Before shipping a credentialed package, confirm:

- [ ] `module` is present and fully qualified
- [ ] no secret values appear anywhere in the manifest
- [ ] each credential has a stable `name`
- [ ] each credential has the right `type`
- [ ] `oauth2` credentials declare a valid `provider`
- [ ] `oauth2` credentials declare non-empty `scopes`
- [ ] `inject.hosts` matches the real API host(s)
- [ ] `inject.method` matches the API’s auth mechanism
- [ ] `headerName` / `queryName` are present only when required
- [ ] `allowed_hosts` is declared and matches real outbound needs
- [ ] tool-level `credentials` is omitted, empty, or replacement **intentionally**
- [ ] the tool code makes normal `fetch()` calls and does not accept token-shaped params

---

## 14. See also

- `docs/pkg-tool-definition-spec.md`
- `docs/rfc-oauth-transport.md`
- `audit/README.md`
- `testutil/fixtures/toolbox.pkgs/github-issues/toolbox.devpkg.json`
- `testutil/fixtures/toolbox.pkgs/google-workspace/toolbox.devpkg.json`
