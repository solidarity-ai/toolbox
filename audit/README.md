# audit

## Purpose

`audit` defines the shared, secret-safe event vocabulary emitted by execution-related packages. It owns event names, payload shapes, validation, and sink/collector helpers for best-effort observation. It does **not** own runtime control flow, transport policy decisions, storage backends, or orchestration.

## Shipped S08 event vocabulary

### `credential_injected`
Emitted by shared transport code after auth material is successfully applied to an outgoing request.

Allowed payload fields:
- `host`
- `credential`
- `inject_method`

### `credential_refresh`
Emitted by shared OAuth2 refresh logic for both success and failure outcomes.

Allowed payload fields:
- `credential`
- `cache_key`
- `outcome` (`success` or `failure`)
- `stage`
- `reason` (failure only)
- `expires_at` (success only)

Current failure stages/reasons are transport-owned strings normalized into audit-safe tokens. The contract future slices must preserve is:
- failures remain classifiable by `stage` and `reason`
- successes carry expiry visibility through `expires_at`
- neither path includes token or secret material

### `credential_denied`
Emitted when transport auth is configured but the request is denied by allowlist policy before mutation/forwarding.

Allowed payload fields:
- `host`
- `reason`
- `credential`
- `inject_method`

## Redaction contract

Audit payloads are intentionally limited to operator-safe metadata. Future changes must preserve these rules:

### Allowed
- hostnames
- credential names / cache identities
- injection methods
- denial reasons
- refresh outcome / stage / reason classification
- expiry timestamps

### Forbidden
Never add any field or derived value that can reveal:
- `Authorization` header contents
- bearer tokens, refresh tokens, access tokens, ID tokens, auth codes, API keys, passwords, or basic-auth payloads
- raw query parameter values used for auth injection
- raw provider response bodies or callback payloads
- durable secret values loaded from `secrets.SecretStore`
- request/response headers or bodies when they may contain auth material

If a future feature needs extra observability, prefer adding a coarse classification or stable identifier instead of serializing raw data.

## Sink and collector usage

`audit` uses a best-effort sink model so request execution stays unchanged when no collector is configured.

- `audit.Sink` is the consumer interface.
- `audit.NoopSink` is the safe default.
- `audit.SinkOrNoop(...)` guarantees callers always get a non-nil sink.
- `audit.Emit(...)` / `audit.TryEmit(...)` must not change runtime behavior when auditing is absent or fails.
- `audit.Collector` is the in-memory test helper for deterministic assertions.

Typical pattern:

1. Construct a sink (or leave it nil and let the caller fall back to noop).
2. Thread the sink into shared seams such as transport policy / injector setup.
3. Emit only validated `audit.Event` values.
4. In tests, use `audit.NewCollector()` and assert exact event ordering/payloads.

## How to inspect the S08 contract

- Run `go test ./cmd/toolbox -run 'TestRunAuth.*' -count=1`
- Run `go test ./oauthbootstrap -run 'TestBootstrap.*' -count=1`
- Run focused transport parity tests when changing auth injection/refresh behavior

Those suites, together with this document, are the canonical contract for audit visibility and operator-facing redaction in S08.
