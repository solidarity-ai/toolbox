# Daemon-Backed OAuth Redirect Objective

## Purpose

Make `toolbox auth` use the running Toolbox daemon as the preferred OAuth 2 redirect coordinator when the daemon is available, while preserving the current local callback/manual-paste behavior as a safe fallback.

The implementation should proceed in small vertical slices. Each phase must produce behavior that can be proven end-to-end before moving to the next phase. Avoid broad horizontal rewrites, speculative abstractions, and plumbing that is not needed by the next observable behavior.

## Guiding Principles

- Prefer small vertical, provable slices over horizontal modules.
- Keep the code simple. Add only the abstractions needed by the current slice.
- Maintain unusually high code quality: clear ownership, minimal coupling, readable state transitions, precise errors, and tests that describe behavior.
- Use outside-in, Kent Beck-style TDD where practical: write behavioral tests that exercise the user-visible path across components, then implement the simplest code that passes.
- Favor existing repository patterns for daemon integration tests, OAuth auth tests, Connect service tests, and HTTP daemon page tests.
- Preserve existing behavior when the daemon is unavailable or daemon OAuth is not usable.
- Keep browser-facing HTTP endpoints thin; authenticated daemon control must go through Connect over the verified Unix-socket transport.
- Do not move token exchange or secret storage into the daemon in this objective. The CLI remains responsible for OAuth config, token exchange, and storing refresh tokens.


## Progress Tracking Instructions

This file is the living progress tracker for the implementation agent. Update it as work proceeds. Do not treat it as a static planning document.

- Before starting a phase, add an entry to the Progress Log describing the intended slice and expected proof.
- As requirements and verification steps are completed, tick the corresponding checkboxes in this file.
- Before ticking any checkbox, pause and reflect on why the item might **not** actually be complete. Look for missing edge cases, unproven integration points, stale UI state, fallback regressions, race conditions, and tests that pass without proving the behavior.
- If that reflection reveals doubt, remediate first: improve the implementation, strengthen the behavioral test, or split the checkbox into smaller concrete checks.
- Only tick a checkbox after the reflection has been performed and the item is genuinely proven.
- Add concise Progress Log entries for important decisions, failed attempts, test results, and any deviations from this objective.


## Anti-Overcomplication Guardrails

The implementing agent is expected to be smart and capable, but must bias strongly toward simple, direct code. Do not generalize before the second or third concrete use case appears.

Hard guardrails:

- Do not introduce a generic workflow engine, task registry, event bus, plugin system, actor model, or broad state-machine framework.
- Do not add a new persistence layer for OAuth flows. Pending OAuth flows are short-lived in-memory daemon state.
- Do not add a database, file format, migration, or durable recovery mechanism for OAuth flow state.
- Do not move OAuth token exchange, client secret ownership, or refresh-token storage into the daemon.
- Do not redesign the auth CLI command model while implementing this objective.
- Do not redesign the daemon web UI. Add the smallest existing-page card needed to expose pending OAuth flows.
- Do not create a dedicated OAuth frontend app, router, template system, or asset pipeline. Use existing daemon page/template patterns.
- Do not replace existing local callback/manual paste behavior. Keep it as fallback.
- Do not make daemon OAuth mandatory. The daemon path is opportunistic and must fail back gracefully.
- Do not add new global configuration beyond `TOOLBOX_HOST` unless a verification step proves it is required.
- Do not log authorization URLs except in intentional user-facing output needed to complete the flow. Never log codes, tokens, client secrets, or refresh tokens.
- Do not add sleeps to tests as synchronization except where existing test patterns already require polling; prefer channels, contexts, fake clocks, or observable state.
- Do not use broad mocks that prove only that mocks were called. Prefer tests that start real daemon/connect/http components where practical.
- Do not scatter new mutexes across services, handlers, client code, or templates. Synchronization belongs behind a narrow owner API.
- Do not tick checkboxes based only on unit tests if the requirement is cross-component behavior. Add an outside-in behavioral test.

Preferred implementation style:

- Prefer existing daemon synchronization/state-notification patterns before introducing new synchronization. Keep synchronization localized inside the OAuth flow manager; do not litter mutexes through call sites or UI code. If a new mutex is necessary, it should be private to one small struct with a clearly documented invariant.
- Reuse existing sync primitives and lifecycle patterns where they fit, such as localized manager-owned state, channels for waiters, contexts for cancellation, and existing notifier patterns.
- Keep browser HTTP callback parsing thin and delegate state changes to the daemon OAuth manager/service.
- Keep Connect request validation boring and explicit.
- Prefer plain errors with clear messages over elaborate error taxonomies, except where Connect status codes are needed.
- Add interfaces only at package boundaries that already need them for existing daemon/client/test patterns.
- Delete temporary helpers once the vertical slice no longer needs them.
- If tempted to add an abstraction, first write down the second concrete caller/use case in the Progress Log. If there is no second caller, do not add it.

Complexity budget:

- Each phase should be reviewable as a small vertical change. If a phase grows too large, split it in the Progress Log before continuing.
- A phase is too large if it changes unrelated UX, storage, daemon lifecycle, or auth command semantics beyond what that phase requires.
- Prefer shipping a boring, well-tested path with fallback over a clever complete framework.

## Agreed Design

### High-level behavior

When `toolbox auth` runs an OAuth 2 flow:

1. The CLI checks whether the daemon is reachable through the existing daemon client/Connect path.
2. If daemon OAuth is available, the CLI uses the daemon-backed OAuth receiver.
3. If daemon OAuth is unavailable, incomplete, invalidly configured, or errors before the flow begins, the CLI falls back to the existing per-command localhost callback receiver.
4. In daemon-backed mode, the daemon web UI shows a pending OAuth authorization card with a clickable provider authorization link.
5. The CLI also prints useful terminal instructions and races daemon callback completion against manual paste input.
6. The CLI still exchanges the authorization code for tokens and stores the refresh token exactly as it does today.

### Control-plane vs browser-plane split

Authenticated/control actions must use Connect over the existing verified daemon Unix socket:

- request advertised daemon OAuth URLs
- begin/register an OAuth flow
- wait for completion
- cancel/cleanup a flow

Browser-facing HTTP remains on the existing daemon webserver:

- `GET /oauth2/callback`
- success/error HTML responses only

The HTTP callback route must not create arbitrary flows. It must only complete states previously registered through authenticated Connect calls.

### Connect API shape

Add a separate `OAuthService` to `daemon/apiv1/daemon.proto`, rather than adding OAuth methods to `SessionService`.

The service should include, at minimum:

```proto
service OAuthService {
  rpc RedirectURI(OAuthRedirectURIRequest) returns (OAuthRedirectURIResponse);
  rpc Begin(OAuthBeginRequest) returns (OAuthBeginResponse);
  rpc Wait(OAuthWaitRequest) returns (OAuthWaitResponse);
  rpc Cancel(OAuthCancelRequest) returns (OAuthCancelResponse);
}
```

Recommended message shape:

```proto
message OAuthRedirectURIRequest {}

message OAuthRedirectURIResponse {
  string redirect_uri = 1;
  string daemon_url = 2;
}

message OAuthBeginRequest {
  string state = 1;
  string authorization_url = 2;
  string label = 3;
}

message OAuthBeginResponse {
  string flow_id = 1;
  google.protobuf.Timestamp expires_at = 2;
}

message OAuthWaitRequest {
  string flow_id = 1;
}

message OAuthWaitResponse {
  string code = 1;
  string state = 2;
  string error = 3;
  string error_description = 4;
  string error_uri = 5;
}

message OAuthCancelRequest {
  string flow_id = 1;
}

message OAuthCancelResponse {}
```

`expires_at` must use `google.protobuf.Timestamp`.

### URL rules

The daemon should advertise OAuth URLs as follows:

- Default redirect URI:

  ```text
  http://localhost:<actual-daemon-webserver-port>/oauth2/callback
  ```

- Default daemon UI URL:

  ```text
  http://localhost:<actual-daemon-webserver-port>/
  ```

- If `TOOLBOX_HOST` is set, it replaces the advertised origin completely, including scheme:

  ```text
  TOOLBOX_HOST=https://example.ngrok.app
  redirect_uri = https://example.ngrok.app/oauth2/callback
  daemon_url   = https://example.ngrok.app/
  ```

- `TOOLBOX_HOST` affects advertised URLs only. It must not affect bind behavior.
- Bind behavior remains controlled by existing daemon bind configuration, including `TOOLBOX_BIND_ADDRESS`.
- Trim whitespace and trailing slashes from `TOOLBOX_HOST`.
- Require `TOOLBOX_HOST` to start with `http://` or `https://`. If invalid, daemon OAuth should fail cleanly and the CLI should fall back to the existing local receiver.

### Flow identity

Use both a daemon-generated `flow_id` and the OAuth `state`:

- `flow_id` is the primary control-plane handle for `Wait` and `Cancel`.
- `state` is used by the browser callback to find the pending flow.
- The daemon must reject unknown, missing, expired, or already-consumed states at `/oauth2/callback` with a friendly HTML error page.
- Multiple concurrent OAuth flows must be supported without races.

### Expiration and cleanup

- Pending daemon OAuth flows expire after 5 minutes, matching the current CLI OAuth timeout.
- Expired flows must be automatically removed from daemon state and disappear from the UI.
- Waiters must unblock with an expired/deadline-style error.
- Late callbacks for expired flows must show a friendly expired/unknown-flow page and must not recreate state.
- `Cancel` must be idempotent.
- The daemon receiver's `Close()` must call `Cancel` when it has a `flow_id`, so manual-paste wins and interrupted flows remove stale UI cards.

### UI behavior

Pending OAuth flows should appear on the existing daemon index/approval console page, not on a new dedicated page.

- Show all pending OAuth flows while the secret store is unlocked.
- Do not show pending OAuth cards while the secret store is locked or setup is required.
- Internally pending flows may remain alive while locked; they should appear if the store becomes unlocked before expiry.
- Each visible OAuth card should include a human-readable label, created/expiry context, and a normal clickable provider authorization link.
- The link should be a normal anchor using the authorization URL, with safe attributes such as `target="_blank"` and `rel="noopener noreferrer"`.

Phase 1 should use a single human-readable `label` string in `OAuthBeginRequest`, composed by the CLI. Avoid adding structured provider/package/account fields until they are needed.

Recommended label:

```text
Authorize <credential-name> for <package-name-or-key> (<account>)
```

If provider name is available and easy to include:

```text
Authorize <credential-name> for <package-name-or-key> (<account>) via <provider-name>
```

### CLI behavior

Daemon-backed OAuth should still race against manual paste input.

- Daemon receiver waits for daemon callback through Connect.
- Manual receiver accepts full redirect URL or code as today.
- `RaceReceiver` chooses the first successful result.
- If manual paste wins, daemon flow must be canceled and removed from UI.

In daemon-backed mode, the CLI may try to open the daemon UI page. Browser-open failure is non-fatal.

The CLI should print instructions along these lines:

```text
Authorization is waiting in the Toolbox daemon.

Opening daemon page...
If it does not open, visit:
<daemon_url>

Then click Continue authorization.

Or visit directly:
<authorization_url>

Or paste the full redirect URL or authorization code here:
```

In local fallback mode, preserve the current behavior of opening the provider authorization URL and using the per-command callback server.

### `oauth2flow` changes

Add the smallest optional hook needed for receivers that must register/start authorization after the final auth URL is built but before waiting for the code.

Recommended interface:

```go
type AuthorizationStarter interface {
    StartAuthorization(ctx context.Context, state string, authorizationURL string) error
}
```

`AuthorizeCode` should:

1. get `receiver.RedirectURI()`
2. generate state
3. build final authorization URL
4. if the receiver implements `AuthorizationStarter`, call `StartAuthorization(ctx, state, authorizationURL)`
5. call `onAuthURL(authorizationURL)`
6. call `receiver.ReceiveCode(ctx, state)`

Existing receivers should not need to change.

`RaceReceiver` should implement `AuthorizationStarter` by calling `StartAuthorization` on child receivers that support it. This allows daemon+manual composition without special casing in `authOAuth2`.


## Advice to the Implementing Agent

1. Keep daemon OAuth as a thin coordinator, not an auth subsystem.

   The daemon should coordinate redirect/wait/UI state. It should not learn about token exchange, refresh-token storage, package credential internals, or auth policy. Protect this boundary carefully.

2. Make the first implementation deliberately boring.

   One in-memory owner for flows. One Connect service. One HTTP callback route. One UI card on the existing page. One daemon receiver in the CLI. No generalized pending-action framework.

3. Treat fallback as part of the feature, not an error path.

   The product behavior is: try daemon-backed OAuth if available; otherwise existing local OAuth still works. Fallback needs first-class behavioral tests. If fallback is only manually checked, it will rot.

4. Be careful with the sequencing around `AuthorizeCode`.

   The new `AuthorizationStarter` hook is a small but central seam. The ordering should be:

   ```text
   RedirectURI -> generate state -> build auth URL -> StartAuthorization -> onAuthURL -> ReceiveCode
   ```

   If that sequence gets wrong, callback races or UI cards with bad URLs can reappear.

5. Do not treat the daemon webserver as trusted.

   The browser-facing HTTP route is not authenticated. The trust boundary is:

   - Connect over verified UDS: trusted local CLI control
   - HTTP callback: untrusted browser/provider input

   `/oauth2/callback` should only complete pre-registered state and should validate input boringly.

6. Make expiry/cancel semantics simple and ruthless.

   A flow is pending, completed, canceled, or expired. Once terminal, it should not be resurrected. Late callbacks get friendly errors. `Cancel` is idempotent. Expired flows disappear.

7. Do not overfit the UI.

   A small card on the existing page is enough:

   ```text
   Authorization needed
   <label>
   [Continue authorization]
   Expires at ...
   ```

   No dedicated page, no OAuth dashboard, no generalized notifications unless later requirements prove they are needed.

8. Use tests as design pressure.

   If a design is hard to test outside-in, it is probably too abstract or too coupled. Follow the repository's existing daemon HTTP/auth test patterns.

9. Keep `TOOLBOX_HOST` boring and explicit.

   Prefer preserving paths if supplied, because it supports reverse proxies:

   ```text
   TOOLBOX_HOST=https://example.test/toolbox
   => https://example.test/toolbox/oauth2/callback
   ```

   Whatever behavior is chosen, encode it once in tests and keep URL construction centralized.

10. Add one golden-path integration test as early as possible.

    The most important proof is:

    ```text
    fake provider -> CLI auth -> daemon UI/callback -> code returned -> token exchanged -> refresh token stored
    ```

    Even if the first version is rough, that test will keep the vertical slice honest.

## Vertical Slice Phases

Each phase below should be completed with outside-in behavioral tests first where practical. Unit tests are acceptable for small pure functions or difficult edge cases, but they should not substitute for behavioral proof of the slice.

### Phase 1 — Prove proto generation and add empty OAuth service wiring

#### Requirements

- [x] Add a separate `OAuthService` to `daemon/apiv1/daemon.proto`.
- [x] Add the agreed OAuth request/response messages.
- [x] Regenerate Connect/protobuf Go code using the repository's Connect generator workflow.
- [x] Add an OAuth service implementation in `daemon/internal/server` and register its handler in the daemon Connect mux.
- [x] Add daemon client plumbing sufficient to call the new service.
- [x] Keep behavior minimal: methods may initially return unimplemented/unavailable where no state exists, but the service must be reachable through the verified daemon Connect path.
- [x] Do not modify OAuth CLI behavior in this phase.

#### Verification steps

- [x] Run proto generation and confirm generated files are updated and compile.
- [x] Run `go test ./daemon/...` and relevant `cmd/toolbox` daemon tests.
- [x] Add a behavioral test that starts a daemon server over its normal test transport and proves an OAuthService method can be called through the daemon client/Connect path.
- [x] Confirm no browser-facing HTTP OAuth route exists yet or, if introduced as a placeholder, it does not accept callbacks.

### Phase 2 — Advertise correct daemon OAuth URLs

#### Requirements

- [x] Implement `OAuthService.RedirectURI`.
- [x] The service must return both `redirect_uri` and `daemon_url`.
- [x] Use the actual daemon webserver listener port.
- [x] Default advertised origin must be `http://localhost:<actual-port>`.
- [x] If `TOOLBOX_HOST` is set, use it as the complete advertised origin.
- [x] `TOOLBOX_HOST` must be trimmed and validated as `http://` or `https://`.
- [x] Invalid `TOOLBOX_HOST` must cause a clean daemon OAuth error, not malformed URLs.
- [x] If the daemon webserver is unavailable/disabled, `RedirectURI` must fail cleanly so the CLI can fall back later.

#### Verification steps

- [x] Behavioral test: start daemon debug webserver on `127.0.0.1:0`, call `OAuthService.RedirectURI`, and assert URLs use `localhost:<actual-port>` and not `127.0.0.1:0`.
- [x] Behavioral test: set `TOOLBOX_HOST=https://example.test/base/`, call `RedirectURI`, and assert:
  - `redirect_uri == https://example.test/base/oauth2/callback` if paths are intentionally preserved, or `https://example.test/oauth2/callback` if implementation chooses origin-only semantics. Choose and document one behavior before implementing.
  - `daemon_url` is the corresponding advertised UI URL.
- [x] Behavioral test: invalid `TOOLBOX_HOST=not-a-url` makes `RedirectURI` return an error.
- [x] Run daemon and auth-related test packages.

### Phase 3 — Register, wait, cancel, and expire daemon OAuth flows through Connect

#### Requirements

- [x] Implement daemon OAuth flow manager in `daemon/internal/server`.
- [x] `Begin` must validate non-empty `state`, valid absolute `authorization_url`, and optional label.
- [x] `Begin` must create a daemon-generated random `flow_id` and return `expires_at` as `google.protobuf.Timestamp`.
- [x] Maintain indexes by `flow_id` and `state`.
- [x] `Wait(flow_id)` must block until callback completion, cancellation, expiry, or caller context cancellation.
- [x] `Cancel(flow_id)` must be idempotent and must unblock waiters.
- [x] Flows must expire automatically after 5 minutes and be removed from state.
- [x] Unknown states must not be implicitly created.
- [x] Multiple concurrent flows with distinct states must not race or cross-complete.
- [x] Keep this state manager simple. Prefer a mutex plus maps/channels over elaborate abstractions unless tests prove more is needed.

#### Verification steps

- [x] Behavioral Connect test: begin a flow, wait in a goroutine, complete it through an internal server method, and assert `Wait` returns the correct code/state.
- [x] Behavioral Connect test: cancel a flow while waiting and assert waiter unblocks and pending state is removed.
- [x] Behavioral Connect test: two simultaneous flows complete independently by state and return their own codes.
- [x] Behavioral test with injectable clock/short TTL or controlled expiry: expired flow is removed, waiter unblocks, and callback after expiry is rejected.
- [x] Run `go test ./daemon/...`.

### Phase 4 — Add browser-facing `/oauth2/callback` route

#### Requirements

- [x] Add `GET /oauth2/callback` to the existing daemon webserver in `cmd/toolbox/daemon.go`.
- [x] The route must parse successful callbacks containing `code` and `state`.
- [x] The route must parse provider error callbacks containing `error`, `error_description`, and `error_uri`.
- [x] The route must require a pre-registered known state.
- [x] Missing state, unknown state, expired state, missing code/error, and repeated callbacks must return friendly HTML error pages.
- [x] Successful callbacks must complete the daemon flow and return a friendly success page.
- [x] Provider-error callbacks must complete the daemon flow with structured error data and return a friendly error/canceled page.
- [x] The route should remain thin: parse HTTP, call the OAuth control/state manager, render response.

#### Verification steps

- [x] Outside-in HTTP + Connect test: begin a flow through Connect, call `/oauth2/callback?code=abc&state=<state>`, then assert `Wait(flow_id)` returns code `abc`.
- [x] HTTP test: unknown state returns non-2xx friendly HTML and does not create a flow.
- [x] HTTP + Connect test: provider error callback unblocks `Wait` with structured `error`, `error_description`, and `error_uri`.
- [x] HTTP test: missing state or missing both code and error returns a useful error page.
- [x] HTTP test: repeated callback does not overwrite the original result.
- [x] Run relevant daemon webserver tests.

### Phase 5 — Show pending OAuth cards on the existing daemon page

#### Requirements

- [x] Expose pending OAuth flows to the existing daemon page state through the in-process `daemonHTTPControl` path or a small extension interface.
- [x] Show all pending OAuth flows on `/`, `/index.html`, and `/approval-console` while the secret store is unlocked.
- [x] Do not show OAuth cards while the secret store is locked, setup-required, or otherwise unavailable.
- [x] Each visible card must include label, useful created/expiry context, and clickable authorization link.
- [x] Expired or canceled flows must disappear automatically.
- [x] Keep UI changes minimal and consistent with existing approval console styles/templates.

#### Verification steps

- [x] Existing daemon page behavioral test: with unlocked secret store and a pending OAuth flow, rendered HTML contains the label and authorization URL.
- [x] Existing daemon page behavioral test: with locked secret store and a pending OAuth flow, rendered HTML does not contain the OAuth label or authorization URL.
- [x] Existing daemon page/events test if applicable: Datastar/SSE updates remove a flow after cancellation/expiry.
- [x] Test that multiple pending flows are all visible while unlocked.
- [x] Run `go test ./cmd/toolbox` or the narrow package tests that cover daemon page rendering.

### Phase 6 — Add `oauth2flow.AuthorizationStarter` and daemon receiver

#### Requirements

- [x] Add optional `AuthorizationStarter` hook to `oauth2flow`.
- [x] Update `AuthorizeCode` to call the hook after building the final authorization URL and before `onAuthURL`/`ReceiveCode`.
- [x] Update `RaceReceiver` to forward `StartAuthorization` to child receivers that support it.
- [x] Implement a daemon-backed `CodeReceiver` used by the CLI.
- [x] The daemon receiver must:
  - get redirect URI and daemon URL through Connect
  - return daemon redirect URI from `RedirectURI()`
  - call `OAuthService.Begin` from `StartAuthorization`
  - wait through `OAuthService.Wait(flow_id)`
  - return provider errors as authorization errors consistent with current behavior
  - call idempotent `OAuthService.Cancel` from `Close()` once it has a `flow_id`
- [x] Keep local `CallbackReceiver`, `ManualReceiver`, and existing behavior intact.

#### Verification steps

- [x] `oauth2flow` behavioral test: a fake receiver implementing `AuthorizationStarter` observes the generated state and authorization URL before `ReceiveCode` is called.
- [x] `RaceReceiver` behavioral test: starter hook is forwarded to daemon child while manual child remains unaffected.
- [x] Daemon receiver integration test: with a test daemon OAuth service, `AuthorizeCode` registers a flow, waits, receives a code, and closes cleanly.
- [x] Manual-wins test: daemon flow is canceled when manual receiver wins the race.
- [x] Run `go test ./oauth2flow ./cmd/toolbox ./daemon/...` as appropriate.

### Phase 7 — Integrate daemon-backed OAuth into `toolbox auth` with fallback

#### Requirements

- [x] Modify `cmd/toolbox/auth.go` so OAuth auth prefers daemon-backed receiver only when daemon OAuth is reachable and returns valid URLs.
- [x] If daemon connection, OAuth service, redirect URI, begin, or pre-flow validation fails, fall back to the existing local callback receiver.
- [x] In daemon-backed mode, race daemon receiver against manual paste.
- [x] In fallback mode, preserve current local callback + manual paste behavior.
- [x] In daemon-backed mode, the CLI may try to open `daemon_url`; failure must be logged or ignored as non-fatal.
- [x] In daemon-backed mode, print instructions that include daemon UI URL, direct authorization URL, and manual paste prompt.
- [x] In fallback mode, preserve current provider browser opening and messaging unless intentionally improved by tests.
- [x] Token exchange and refresh-token storage must remain in the CLI.
- [x] Existing OAuth tests must continue to pass, updated only where the new preferred daemon behavior is explicitly under test.

#### Verification steps

- [x] End-to-end auth test with daemon available:
  - start daemon webserver/Connect server
  - run OAuth auth flow against a fake provider
  - assert daemon page has pending OAuth card
  - complete provider redirect to daemon `/oauth2/callback`
  - assert CLI stores refresh token
- [x] End-to-end auth test with daemon unavailable:
  - run existing fake-provider OAuth flow
  - assert local callback receiver path still works and stores refresh token.
- [x] End-to-end auth test where daemon OAuth `RedirectURI` errors:
  - assert CLI falls back to local receiver.
- [x] Manual-paste test in daemon-backed mode:
  - paste code/redirect manually
  - assert token storage succeeds
  - assert daemon flow is canceled/removed.
- [x] Browser-open failure test:
  - make daemon UI open fail
  - assert auth still proceeds via printed/direct/manual path.

### Phase 8 — Hardening, cleanup, and documentation polish

#### Requirements

- [x] Review all new errors for clarity and absence of secret leakage.
- [x] Ensure authorization URLs are displayed only where necessary and never logged unexpectedly beyond user-facing terminal/UI surfaces.
- [x] Ensure no stale goroutines/timers remain after wait/cancel/expiry.
- [x] Ensure daemon shutdown cleans up waiters cleanly.
- [x] Keep API and code names consistent and small.
- [x] Remove any temporary test-only hooks that are no longer needed.
- [x] Add concise comments only where they explain non-obvious concurrency, security, or URL-advertising decisions.
- [x] Do not introduce broad frameworks or generic flow managers beyond this OAuth need.

#### Verification steps

- [x] Run `go test ./...`.
- [x] Run targeted race-sensitive tests with `-race` if feasible for daemon/oauth packages.
- [x] Manually inspect generated proto/connect diffs for expected service additions only.
- [x] Manually inspect daemon web UI HTML for locked/unlocked behavior and link safety.
- [x] Review fallback paths by forcing:
  - daemon unavailable
  - invalid `TOOLBOX_HOST`
  - expired flow
  - provider error callback
  - manual paste wins

## Open Implementation Detail to Resolve Before Phase 2

`TOOLBOX_HOST` with a path needs one explicit decision before implementation:

- Option A: preserve the path as a base path, so `TOOLBOX_HOST=https://example.test/toolbox` yields `https://example.test/toolbox/oauth2/callback` and `https://example.test/toolbox/`.
- Option B: treat `TOOLBOX_HOST` as an origin only, ignoring paths or rejecting URLs with paths.

Recommendation: preserve the path if supplied, because the user said `TOOLBOX_HOST` replaces everything including scheme. This is useful for reverse proxies mounted under a path. Trim trailing slashes before appending routes.

## Progress Log

Agents should append dated entries here as they work. Keep entries concise but specific enough for the next agent to understand what changed, what was proven, and what remains uncertain.

Template:

```text
YYYY-MM-DD HH:MM — Phase N — short summary
- Changed: files/components touched
- Proved: tests or manual verification run
- Reflected: why this might not be done, and what was remediated
- Next: immediate next step
```

Entries:

- 2026-05-26 23:24 — Phase 1 — start empty OAuthService wiring slice
  - Changed: planning entry only so far.
  - Proved: baseline `go test ./daemon/...` was green before code changes.
  - Reflected: Phase 1 is not complete yet; current repo has no OAuth proto/service/client wiring and no behavioral reachability test.
  - Next: add proto/messages, regenerate generated code, register an unimplemented daemon service, add daemon client plumbing, and prove it is reachable through the verified daemon transport.
- 2026-05-26 23:39 — Phase 1 — empty OAuthService reachable through daemon client
  - Changed: `daemon/apiv1/daemon.proto`, generated `daemon.pb.go`/`daemon.connect.go`, `daemon/internal/server/oauth_service.go`, server mux wiring, daemon client OAuth plumbing, public daemon aliases, and `daemon/daemon_test.go`.
  - Proved: ran `PATH="$PATH:$HOME/go/bin" protoc -I. --go_out=. --go_opt=paths=source_relative --connect-go_out=. --connect-go_opt=paths=source_relative daemon/apiv1/daemon.proto`; `go test ./daemon/...`; `go test ./cmd/toolbox -run 'Daemon|daemon'`.
  - Reflected: Phase 1 could be falsely complete if the route existed only in generated code or only direct service calls were tested. Remediated with `TestOAuthServiceReachableThroughVerifiedDaemonClient`, which starts the normal daemon test server and calls `client.OAuthRedirectURI`; it receives the service's intentional `Unavailable` rather than an HTTP route-not-found. No `/oauth2/callback` route was added and OAuth CLI files were not touched.
  - Next: Phase 2 URL advertisement, including actual daemon webserver port and `TOOLBOX_HOST` handling.
- 2026-05-26 23:43 — Phase 2 — start RedirectURI URL advertisement slice
  - Changed: planning entry only so far.
  - Proved: Phase 1 test suite was green before this slice.
  - Reflected: URL advertisement can look correct while still using the bind address (`127.0.0.1:0`) or ignoring reverse-proxy base paths. This slice will add behavior tests for actual listener port, `TOOLBOX_HOST` path preservation, invalid host errors, and webserver-disabled fallback.
  - Next: implement centralized URL builder plus daemon HTTP address handoff from the browser webserver to the Connect OAuth service.
- 2026-05-26 23:54 — Phase 2 — RedirectURI advertises actual webserver URLs
  - Changed: `daemon/internal/server/oauth_service.go` now builds redirect/UI URLs, preserves `TOOLBOX_HOST` base paths, validates schemes, and fails cleanly when no webserver address is registered; `cmd/toolbox/daemon.go` registers the actual debug listener address with the daemon control; added daemon and cmd behavioral tests.
  - Proved: `go test ./daemon/...`; `go test ./cmd/toolbox -run 'OAuth|Daemon|daemon'`.
  - Reflected: The phase could be under-proven if only pure URL helpers were tested. Added Connect/daemon tests (`TestOAuthRedirectURIUsesRegisteredDaemonWebserverAddress`, `TestOAuthRedirectURIPreservesToolboxHostBasePath`, invalid host/unavailable tests) and a cmd-level integration test (`TestDaemonDebugServerAdvertisesOAuthURLsThroughConnect`) that starts a Unix-socket daemon plus debug webserver on `127.0.0.1:0` and calls through `daemon.EnsureConnection`, proving localhost + actual port behavior. Chose and encoded path-preserving `TOOLBOX_HOST` semantics.
  - Next: Phase 3 in-memory flow manager for Begin/Wait/Cancel/expiry.
- 2026-05-27 00:02 — Phase 3 — start Connect-managed OAuth flow lifecycle slice
  - Changed: planning entry only so far.
  - Proved: Phase 2 targeted daemon and cmd tests were green before this slice.
  - Reflected: Flow lifecycle can appear complete if direct manager calls pass while Connect wait/cancel paths, waiter wakeups, expiry cleanup, and state/flow indexes diverge. This slice will add behavioral Connect tests that begin through the daemon client path, wait concurrently, complete via a narrow internal server hook, cancel, handle multiple flows, and prove deterministic expiry without sleeps.
  - Next: implement a small in-memory OAuth flow manager owned by `daemon/internal/server.OAuthService`, wire Begin/Wait/Cancel, expose the narrow completion hook, and run `go test ./daemon/...`.
- 2026-05-27 00:31 — Phase 3 — Connect OAuth flow lifecycle implemented
  - Changed: added `daemon/internal/server/oauth_flow_manager.go` with an in-memory mutex/map/channel flow owner; wired `OAuthService.Begin`, `Wait`, `Cancel`, and the narrow `CompleteOAuthFlow` internal hook; added daemon client Begin/Wait/Cancel helpers and public aliases.
  - Proved: `go test ./daemon/...`; `go test ./cmd/toolbox -run 'OAuth|Daemon|daemon'`. Added Connect tests for begin/wait/complete, cancel/idempotent cleanup, independent concurrent flows, validation/duplicate states, unknown-state completion, and controlled manager expiry/default 5-minute TTL.
  - Reflected: Phase 3 could be falsely complete if waiters were tested by sleeping or if completed/canceled states stayed callable. Tests synchronize with a manager-owned waiter hook, assert late completion fails after cancel/expiry/completion, assert unknown callback does not create state, and cover `expires_at`/default TTL. State remains in-memory only; no browser callback route or CLI OAuth behavior was added.
  - Next: Phase 4 browser-facing `/oauth2/callback` route on the daemon webserver.
- 2026-05-27 00:34 — Phase 4 — start browser callback route slice
  - Changed: planning entry only so far.
  - Proved: Phase 3 daemon and cmd OAuth/daemon tests were green before this slice.
  - Reflected: The callback route can look done if it parses query strings but bypasses authenticated registration or creates flows implicitly. This slice will keep the HTTP handler thin, complete only pre-registered states via the daemon control hook, return friendly HTML for success/error/failure, and prove behavior with HTTP + Connect tests.
  - Next: inspect existing daemon webserver routing/template patterns, add `/oauth2/callback`, and run relevant daemon webserver tests.
- 2026-05-27 00:58 — Phase 4 — browser OAuth callback route completed
  - Changed: `cmd/toolbox/daemon.go` now registers `/oauth2/callback` before the `/` catch-all, parses success and provider-error callback query parameters, completes only through the daemon OAuth completion hook, and renders escaped friendly HTML without echoing authorization codes. `cmd/toolbox/daemon_test.go` now has HTTP + Connect callback tests and direct HTTP validation tests.
  - Proved: `go test ./cmd/toolbox -run 'OAuth|Daemon|daemon'`; `go test ./daemon/...`; `go test ./cmd/toolbox`.
  - Reflected: Phase 4 could be falsely complete if the route accepted unknown state by creating a flow, if repeated callbacks overwrote results, or if provider errors were only shown in HTML without reaching Connect waiters. Remediated with outside-in tests that begin via daemon Connect, callback via HTTP, and wait via Connect for both success and provider errors; a real-daemon unknown-state test proves callbacks do not reserve/create state; repeated callback keeps the first result; missing/expired/unknown states and missing code/error get non-2xx friendly HTML.
  - Next: Phase 5 pending OAuth cards on the existing daemon page.
- 2026-05-27 00:16 — Phase 5 — start pending OAuth cards slice
  - Changed: planning entry only so far.
  - Proved: Phase 4 daemon/cmd tests were green before this slice per the prior entry.
  - Reflected: UI cards can appear complete while leaking pending authorization links on locked/setup/unavailable pages or failing to disappear after cancel/expiry. This slice will expose only pending flow snapshots through a narrow in-process daemon control method, gate rendering on unlocked non-setup secret store state, and prove index/approval-console/SSE behavior with daemon page tests.
  - Next: add pending-flow snapshots to the daemon OAuth manager/service/server, thread them into approval console state, render minimal cards, and verify with cmd/daemon tests.
- 2026-05-27 00:30 — Phase 5 — pending OAuth cards completed on existing daemon page
  - Changed: added pending-flow snapshots to the daemon OAuth manager/service/server/public alias; extended the daemon HTTP control path and approval-console state with pending OAuth summaries; rendered minimal OAuth cards in the existing approval console templates; regenerated templ output; added cmd/daemon behavioral tests.
  - Proved: `go test ./cmd/toolbox`; `go test ./daemon/...`; targeted real-daemon proof `TestApprovalConsoleShowsRealDaemonPendingOAuthFlow`; manager expiry proof `TestOAuthFlowManagerPendingExpiresDueFlows`.
  - Reflected: Phase 5 could have been falsely complete if only stub UI tests passed, if cards leaked while locked/setup/unavailable, or if canceled/expired flows lingered. Remediated with a real Unix-socket daemon + real secret-store setup test that begins through Connect, renders `/`, `/index.html`, and `/approval-console`, verifies locked suppression/unlocked restoration, cancels through the daemon client, and confirms the page plus `PendingOAuthFlows` are empty. Added controlled-time manager coverage proving `Pending()` expires due flows without relying on sleeps.
  - Next: Phase 6 `oauth2flow.AuthorizationStarter` hook and daemon-backed receiver.
- 2026-05-27 00:32 — Phase 6 — start AuthorizationStarter and daemon receiver slice
  - Changed: planning entry only so far.
  - Proved: Phase 5 `go test ./cmd/toolbox` and `go test ./daemon/...` were green before this slice.
  - Reflected: The hook can appear correct while registering before the final URL exists, after waiting starts, or only for one child in a race; the daemon receiver can appear correct while failing to cancel on manual wins or failing to preserve provider-error semantics. This slice will first add ordering/forwarding tests in `oauth2flow`, then add a thin cmd-level daemon receiver with Connect-backed begin/wait/cancel tests.
  - Next: add `AuthorizationStarter`, update `AuthorizeCode` and `RaceReceiver`, then implement and prove the daemon-backed receiver.
- 2026-05-27 00:44 — Phase 6 — AuthorizationStarter and daemon receiver completed
  - Changed: `oauth2flow` now has the optional `AuthorizationStarter` hook, `AuthorizeCode` calls it after final auth URL construction and before `onAuthURL`/`ReceiveCode`, `RaceReceiver` forwards it to supporting children, and `cmd/toolbox/oauth_daemon_receiver.go` implements the daemon-backed receiver with Connect redirect/begin/wait/cancel behavior.
  - Proved: `go test ./oauth2flow`; `go test ./cmd/toolbox -run DaemonOAuthReceiver`; `go test ./oauth2flow ./cmd/toolbox ./daemon/...`.
  - Reflected: Phase 6 could be falsely complete if the receiver only passed mocks or if manual wins left stale daemon UI cards. Remediated with ordering/forwarding tests, provider-error parity tests, a real daemon + debug-server callback integration test, and both stub and real-daemon manual-wins cancellation tests. Local callback/manual receiver code was not changed; actual `toolbox auth` preference/fallback wiring remains Phase 7.
  - Next: Phase 7 integration into `authOAuth2` with daemon preference and fallback.
- 2026-05-27 00:45 — Phase 7 — start daemon-preferred auth integration slice
  - Changed: planning entry only so far.
  - Proved: Phase 6 targeted and broad tests were green before this slice.
  - Reflected: It would be easy to regress existing local OAuth tests by always trying daemon launch, or to incorrectly fall back after a provider/callback error rather than only pre-flow/registration failures. This slice will add narrow seams and outside-in auth tests for daemon available, daemon unavailable/invalid redirect fallback, manual wins cancellation, and non-fatal daemon UI browser-open failure while keeping token exchange/storage in the CLI.
  - Next: factor receiver selection/instructions in `authOAuth2`, add daemon-preferred tests, then run the targeted and broad suites.
- 2026-05-27 01:18 — Phase 7 — daemon-preferred auth integration completed
  - Changed: `cmd/toolbox/auth.go` now selects daemon-backed OAuth opportunistically, falls back to the local callback/manual receiver before a daemon flow is established, preserves local behavior by default in existing test seams, and keeps token exchange/refresh-token storage in the CLI. `cmd/toolbox/auth_command.go` passes daemon preference for real CLI OAuth paths. `cmd/toolbox/auth_test.go` now has outside-in daemon-available, daemon-unavailable, redirect error/invalid URL, begin failure/empty flow id, manual-wins cancellation, and browser-open failure coverage.
  - Proved: targeted Phase 7 suite; `go test ./cmd/toolbox -run 'TestRunAuthOAuth2|TestDaemonOAuthReceiver' -count=1`; `go test ./oauth2flow ./cmd/toolbox ./daemon/...`.
  - Reflected: Phase 7 could be falsely complete if it only proved the golden daemon path, or if fallback was tested only for connection failures. Added explicit fallback coverage for RedirectURI errors, invalid advertised URLs, Begin errors, and empty flow ids. A broad OAuth run exposed that a late pasted callback URL could already be in `authInput` backlog before `IgnoreOnce`; fixed dispatch to re-check ignore rules before delivering backlog and reproved the callback/next-credential test.
  - Next: Phase 8 hardening: full-suite tests, race-sensitive checks, and manual inspection for leakage, shutdown, generated code, UI link safety, and fallback/error paths.
- 2026-05-27 01:19 — Phase 8 — start hardening and completion-audit slice
  - Changed: planning entry only so far.
  - Proved: Phase 7 targeted and broad OAuth/daemon suites are green before hardening.
  - Reflected: Passing targeted tests is not enough for completion; hardening needs full `go test ./...`, selected `-race` runs, manual diff/security inspection, and an explicit audit against every objective requirement/non-goal.
  - Next: inspect the new daemon/auth/oauth code and generated diffs, run full and race-sensitive tests, remediate anything uncovered, then perform the final checklist audit.
- 2026-05-27 02:34 — Phase 8 — hardening inspection and verification completed
  - Changed: `objective.md` Phase 8 checklist only.
  - Proved: `go test ./...`; `go test -race ./oauth2flow ./daemon/...`; `go test -race ./cmd/toolbox -run 'OAuth|DaemonOAuth|ApprovalConsoleShowsRealDaemonPendingOAuthFlow'`; targeted fallback/error proofs `go test ./daemon -run 'TestOAuthRedirectURIRejectsInvalidToolboxHost|TestOAuthRedirectURIPreservesToolboxHostBasePath'`, `go test ./daemon/internal/server -run 'TestOAuthFlowManagerExpiresFlow|TestOAuthFlowManagerCompleteProviderError'`, and `go test ./cmd/toolbox -run 'TestRunAuthOAuth2FallsBackWhenDaemonUnavailable|TestRunAuthOAuth2FallsBackWhenDaemonRedirectURIErrors|TestRunAuthOAuth2ManualPasteCancelsDaemonFlow|TestDaemonOAuthCallbackProviderError'`.
  - Reflected: Re-ran full and race-sensitive suites and manually inspected `cmd/toolbox/auth.go`, `cmd/toolbox/oauth_daemon_receiver.go`, `daemon/internal/server/oauth_flow_manager.go`, `daemon/internal/server/oauth_service.go`, `oauth2flow/oauth2flow.go`, generated proto/connect diffs, and the generated OAuth card HTML. Authorization URLs appear only in the user-facing CLI direct-visit instructions and daemon UI hrefs; callback success pages do not echo codes. Flow cleanup is localized in the manager with timers stopped on wait/cancel/close and waiters closed on shutdown. The generated proto/connect diff contains the expected `OAuthService` and messages only. UI output is gated by existing unlocked-page state and uses escaped label/URL plus `target="_blank" rel="noopener noreferrer"`. A process-wide forced invalid `TOOLBOX_HOST` makes daemon-preference auth tests fall back as expected, so invalid host was verified with the targeted daemon RedirectURI test rather than treating those daemon-golden auth tests as environment-independent.
  - Next: perform final completion audit against the objective and stop only if no uncovered requirement remains.

## Non-Goals

- Do not move OAuth token exchange into the daemon.
- Do not move refresh-token storage into the daemon OAuth flow.
- Do not add a new dedicated OAuth web page in phase 1.
- Do not require the daemon to auto-open the provider authorization URL.
- Do not remove manual paste fallback.
- Do not remove or regress the existing local callback receiver.
- Do not build a generic workflow engine, generic pending-task system, or broad UI routing abstraction unless later requirements prove it necessary.
