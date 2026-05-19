# Auth CLI Update Objectives

## Purpose

Refresh the `toolbox auth` experience so users can understand, configure, inspect, and repair authentication for tool packages with confidence.

This document describes the desired outcomes and product requirements from a product/stakeholder lens. It intentionally avoids implementation details, exact command syntax, parser choices, storage internals, daemon APIs, or migration mechanics.

## Product Objective

Authentication should become a clear, safe, and discoverable user journey for Toolbox packages.

A user should be able to answer:

- Does this package need authentication?
- What kind of authentication does it need?
- Am I already authenticated?
- Which account or profile is being used?
- How do I log in, log out, configure, rotate, clear, or repair credentials?
- What should I do if the secret store is locked, uninitialized, or unavailable?

The auth CLI should hide unnecessary internals while making security-relevant scope explicit.

## Primary Goals

### 1. Make package authentication understandable

The auth CLI should be organized around user-facing authentication tasks for tool packages, not around internal credential paths or secret-store implementation details.

Users should understand that:

- tool packages may declare authentication requirements
- a package may have one or more credentials
- credentials may be OAuth-based or static secret material such as API keys
- a credential may have one or more accounts/profiles
- authentication state belongs to a specific package context

Success means a user can inspect and satisfy a package’s auth requirements without needing to know how credentials are stored internally.

### 2. Treat OAuth and static secrets as distinct workflows

The CLI should not pretend that all authentication material has the same lifecycle.

OAuth flows and static secret configuration have different user expectations:

- OAuth users expect login, logout, token refresh, and account status.
- Static-secret users expect setting, clearing, rotating, and validating configured values.
- OAuth credentials may also require static setup values, such as client IDs or client secrets, which should be presented clearly as configuration rather than as the token login itself.

The product should make these differences clear while still presenting a unified auth experience.

### 3. Make authentication status easy to inspect

Users should be able to see the auth state of a package before attempting to use it.

Status output should help users understand:

- whether authentication is required
- whether required credentials are present
- whether OAuth credentials are logged in, expired, missing, or invalid
- whether static secrets are configured or missing
- which account/profile is active, where applicable
- whether action is blocked by secret-store state

A broader status view should help users understand auth state across the active toolset when appropriate.

### 4. Preserve clear security boundaries

The auth CLI must not silently blur credential scope.

Users and operators should be able to trust that credentials for one package context are not accidentally reused in another. The product must distinguish between:

- installed tool packages
- overridden or replaced packages
- local package directories used for development
- different credentials declared by the same package
- different accounts/profiles for the same credential

When a command selects or crosses one of these boundaries, the CLI should make that scope explicit before mutating credentials.

### 5. Use package targets as the primary user concept

The main auth target should be a tool package, because packages declare credentials and may contain multiple tools that share those credentials.

The user should not need to authenticate individual tools unless the package model explicitly requires that distinction. Lower-level credential or account selection should appear only when needed to disambiguate.

Local package directories should be treated as a separate, explicit target class so development credentials are not confused with installed package credentials.

### 6. Handle ambiguity safely

The auth CLI should not guess when guessing could affect credential scope or account selection.

If a package has multiple relevant credentials, accounts, or possible target matches, the CLI should guide the user to make an explicit choice.

Helpful ambiguity handling should include:

- explaining what was ambiguous
- showing the available choices
- recommending the next command or option
- avoiding unintended credential creation, overwrite, or deletion

### 7. Make secret-store state actionable

Auth commands depend on the secret store, but users should experience locked, uninitialized, or recovery-required states as expected product states rather than internal failures.

The auth CLI should explain:

- when the secret store must be set up
- when it must be unlocked
- when recovery or backup codes are relevant
- when an operation cannot proceed non-interactively
- what command or action the user should take next

First-time setup must be intentional. If backup or recovery codes are generated, the user must be shown them clearly and understand that they are important.

### 8. Support both interactive users and automation

The auth CLI should work well in terminals and in scripts.

Interactive users should get prompts and guidance where appropriate. Non-interactive environments should receive deterministic errors, stable status signals, and no surprise prompts.

Automation should be able to distinguish important auth states without scraping human prose, including:

- authenticated
- missing credentials
- expired credentials
- locked secret store
- uninitialized secret store
- unsupported auth type
- ambiguous target, credential, or account selection

### 9. Prefer a clean unreleased interface over prototype compatibility

The current auth CLI has not been released, so the updated auth CLI does not need to preserve old command forms, flags, or migration behavior.

The product should choose the clearest long-term shape rather than carrying compatibility costs for internal prototype syntax.

## Product Requirements

### Authentication workflow requirements

- Users must be able to inspect auth status for one package.
- Users should be able to inspect auth status across auth-relevant packages in the active context.
- Users must be able to complete OAuth login for OAuth credentials.
- Users must be able to log out or remove OAuth token material.
- Users must be able to configure static secret material such as API keys.
- Users must be able to clear or rotate static secret material.
- Users must be able to distinguish package-level, credential-level, and account-level auth state.
- The CLI must not silently choose among multiple credentials or accounts when the choice has security or data-access implications.

### Targeting and scope requirements

- The primary auth target should be a tool package.
- Local directory authentication should require explicit user intent.
- Overridden/replaced packages should have clearly defined credential identity.
- The CLI should communicate which package context credentials will be read from or written to.
- Auth commands should avoid exposing internal storage keys as the primary UX.

### Secret and credential safety requirements

- Secret values must not be printed accidentally.
- Secret values should not be encouraged in shell history or process arguments.
- Credential-clearing operations should make their scope clear.
- Error messages must avoid leaking sensitive credential material.
- First-time secret-store setup should not silently create recovery material without showing it to the user.

### Secret-store lifecycle requirements

- Uninitialized secret store should produce an actionable setup-required state.
- Locked secret store should produce an actionable unlock-required state.
- Recovery-required or recovery-window states should be explained in user-facing terms.
- Interactive flows may guide setup/unlock where appropriate.
- Non-interactive flows must fail predictably when required setup/unlock input is unavailable.

### Output and diagnostics requirements

- Human-readable output should be concise, clear, and action-oriented.
- Machine-readable output should be available for automation-sensitive status checks.
- Errors should say what failed, why it failed when known, and what to do next.
- Status should distinguish missing, expired, invalid, configured, and authenticated states where the underlying auth type supports those distinctions.

### Compatibility requirements

- No old auth CLI command forms need to be preserved.
- No old auth CLI command forms need deprecation messaging.
- Prototype auth flags and passthrough argument parsing may be removed.
- Existing unreleased tests and fixtures may be updated to the new command model rather than preserving old behavior.

## Non-Goals

This objective document does not decide:

- the exact auth command tree
- exact flag names or positional arguments
- the CLI parsing library
- credential key naming internals
- on-disk secret-store format
- daemon/client API design
- exact JSON output schemas
- final rollout mechanics
- implementation sequencing

Those should be captured in RFCs, design docs, or implementation plans after the product objectives are agreed.

## Success Criteria

The auth CLI update is successful when:

- a new user can authenticate a package without understanding Toolbox internals
- users can clearly distinguish package, credential, and account/profile concepts
- OAuth and static-secret workflows feel natural and separate where they need to be
- credentials are scoped predictably across installed packages, overrides, and local directories
- ambiguous credential or account situations produce helpful guidance instead of unsafe guessing
- locked or uninitialized secret-store states are actionable rather than opaque
- automated environments can reliably detect auth/setup/locked states
- the implementation does not carry unnecessary compatibility behavior for unreleased prototype commands

---

# Proposed Auth CLI Shape

## Recommendation

The new auth CLI should be package-first for general inspection, but workflow-specific for mutation. In particular, OAuth should be an explicit subcommand namespace rather than hidden behind a generic `login` command.

The canonical shape should be:

```text
toolbox auth
  status [TARGET]
  list
  accounts TARGET

  oauth2
    status TARGET
    configure TARGET
    login TARGET
    logout TARGET
    refresh TARGET

  secret
    status TARGET
    set TARGET
    clear TARGET
    rotate TARGET
    validate TARGET

  setup
  unlock
  lock
  recovery
    codes
    rewrap
```

This keeps the package as the primary user-facing target while making the credential workflow explicit:

- OAuth2 credentials have a login/logout/refresh lifecycle.
- Static secrets have a set/clear/rotate/validate lifecycle.
- Package-level `status` and `list` provide the unified auth view.
- Secret-store lifecycle commands are first-class and actionable.

## Targeting Model

The default target should be an installed package in the active toolset:

```text
toolbox auth status github
toolbox auth oauth2 login github
toolbox auth secret set openai
```

Local development package directories should require explicit intent:

```text
toolbox auth status --local ./package-dir
toolbox auth oauth2 login --local ./package-dir
toolbox auth secret set --local ./package-dir
```

Commands that read or mutate credentials should clearly communicate the resolved package context, especially when the target is local, overridden, replaced, or otherwise not the ordinary installed package.

## General Package-Level Commands

### `toolbox auth status [TARGET]`

Shows the auth state for one package, or, where appropriate, the active auth context.

It should answer:

- whether authentication is required
- which credentials are declared
- whether each credential is configured, missing, expired, invalid, or authenticated
- which accounts/profiles exist
- whether the secret store is locked or uninitialized
- what command the user should run next

For mixed packages, status should recommend workflow-specific next steps:

```text
Package: example

Credential: github-oauth
Type: OAuth2
Status: not logged in
Next step:
  toolbox auth oauth2 login example --credential github-oauth

Credential: api-key
Type: API key
Status: missing
Next step:
  toolbox auth secret set example --credential api-key
```

### `toolbox auth list`

Shows auth-relevant packages in the active toolset.

It should be concise for humans and stable for automation:

```text
PACKAGE      REQUIRED  STATUS           NEXT STEP
github       yes       not logged in    toolbox auth oauth2 login github
openai       yes       configured       -
slack        yes       missing secret   toolbox auth secret set slack
weather      no        not required     -
```

### `toolbox auth accounts TARGET`

Lists accounts/profiles known for a package, grouped by credential.

This should not print secret values.

## OAuth2 Commands

OAuth2 commands apply only to OAuth2 credentials.

### `toolbox auth oauth2 configure TARGET`

Configures OAuth client setup values such as:

- client ID
- client secret
- provider-specific static setup values

These values are configuration for the OAuth app/client and should be presented separately from user token login.

### `toolbox auth oauth2 login TARGET`

Starts or completes an OAuth2 authorization flow and stores token material for an account/profile.

If required OAuth client configuration is missing, interactive use may offer to configure it first. Non-interactive use should fail with a deterministic setup-required error.

### `toolbox auth oauth2 logout TARGET`

Removes OAuth token material for the selected credential/account.

It should not remove OAuth client configuration unless explicitly requested.

### `toolbox auth oauth2 refresh TARGET`

Refreshes OAuth token material where supported.

If refresh is unsupported or the token is invalid, the command should explain whether the user needs to log in again.

### `toolbox auth oauth2 status TARGET`

Shows OAuth-specific status:

- client configuration present/missing
- account logged in/logged out
- token expired, invalid, or refreshable when detectable
- active/default account where applicable

## Static Secret Commands

Static secret commands apply to non-OAuth secret material, including:

- API keys
- bearer tokens
- basic auth username/password
- other static secret types declared by packages

### `toolbox auth secret set TARGET`

Prompts for and stores static secret material.

The primary UX should avoid passing secret values in shell arguments. Preferred input modes:

```text
toolbox auth secret set openai
printf '%s' "$OPENAI_API_KEY" | toolbox auth secret set openai --stdin
toolbox auth secret set openai --from-env OPENAI_API_KEY
```

If an unsafe argument-based value form is supported, it should not be the documented default and should warn where appropriate.

### `toolbox auth secret clear TARGET`

Removes selected static secret material.

Destructive operations should show the affected package, credential, account/profile, and secret labels before proceeding in interactive mode.

### `toolbox auth secret rotate TARGET`

Replaces static secret material.

Rotate should make it clear whether the old value is overwritten immediately and whether validation is available before replacement.

### `toolbox auth secret validate TARGET`

Validates configured static secrets where the credential type or package supports validation.

If validation is unsupported, the command should return a stable unsupported state rather than pretending success.

### `toolbox auth secret status TARGET`

Shows static-secret-specific configuration state without printing values.

## Secret Store Commands

Secret-store lifecycle should be a product-level concept, not an internal failure.

```text
toolbox auth setup
toolbox auth unlock
toolbox auth lock
toolbox auth recovery codes
toolbox auth recovery rewrap
```

Uninitialized, locked, recovery-required, and unavailable states should produce actionable guidance. Non-interactive commands should not prompt unexpectedly.

## Ambiguity and Safety Rules

The CLI must not guess when a guess could affect credential scope or account selection.

Safe to infer:

- showing status for a package with multiple credentials
- listing all possible choices

Not safe to infer:

- clearing credentials when multiple credentials or accounts exist
- logging out when multiple OAuth2 accounts exist
- rotating a static secret when multiple static credentials exist
- writing credentials to a local package when the user did not explicitly choose local targeting

When ambiguous, commands should:

- explain what was ambiguous
- show available choices
- recommend the next command
- avoid creating, overwriting, or deleting credentials

Example:

```text
Error: package "example" declares multiple credentials.

Credentials:
  github-oauth      OAuth2
  api-key           API key

Choose one:
  toolbox auth oauth2 login example --credential github-oauth
  toolbox auth secret set example --credential api-key
```

## Compatibility and Migration

No compatibility layer is required for the previous auth CLI shape because the auth CLI has not been released.

The implementation should prefer the clean proposed command model over preserving internal prototype commands, flags, or argument forms.

---

# Implementation Requirements Checklist

## Command shape

- [x] Add first-class auth subcommands instead of relying only on passthrough auth args.
- [x] Add `toolbox auth status [TARGET]`.
- [x] Add `toolbox auth list`.
- [x] Add `toolbox auth accounts TARGET`.
- [x] Add `toolbox auth oauth2 status TARGET`.
- [x] Add `toolbox auth oauth2 configure TARGET`.
- [x] Add `toolbox auth oauth2 login TARGET`.
- [x] Add `toolbox auth oauth2 logout TARGET`.
- [x] Add `toolbox auth oauth2 refresh TARGET`.
- [x] Add `toolbox auth secret status TARGET`.
- [x] Add `toolbox auth secret set TARGET`.
- [x] Add `toolbox auth secret clear TARGET`.
- [x] Add `toolbox auth secret rotate TARGET`.
- [x] Add `toolbox auth secret validate TARGET`.
- [x] Add `toolbox auth setup`.
- [x] Add `toolbox auth unlock`.
- [x] Add `toolbox auth lock`.
- [x] Add `toolbox auth recovery codes`.
- [x] Add `toolbox auth recovery rewrap`.

## Targeting and scope

- [x] Resolve ordinary auth targets as installed packages in the active toolset.
- [x] Require explicit `--local` for local development package directories.
- [x] Show resolved package context before mutating credentials.
- [x] Distinguish installed packages from overridden/replaced package contexts.
- [x] Avoid exposing internal secret-store keys as primary user-facing identifiers.
- [x] Preserve package-level targeting as the default user concept.
- [x] Support credential-level selection with `--credential`.
- [x] Support account/profile-level selection with `--account`.
- [x] Reject ambiguous mutating commands instead of guessing.
- [x] Provide clear choices and suggested next commands on ambiguity.

## OAuth2 workflow

- [x] Treat `auth oauth2 login` as OAuth2-only.
- [x] Treat `auth oauth2 logout` as OAuth2 token removal, not static secret clearing.
- [x] Separate OAuth2 client configuration from OAuth2 login/token state.
- [x] Prompt for missing OAuth2 client configuration interactively when safe.
- [x] Fail deterministically for missing OAuth2 client configuration in non-interactive mode.
- [x] Preserve existing OAuth2 authorization flow behavior.
- [x] Preserve PKCE public-client behavior.
- [x] Preserve manual paste/headless OAuth2 flow behavior.
- [x] Track OAuth2 account/profile scope explicitly.
- [x] Report OAuth2 logged-in, logged-out, missing, expired, invalid, and unsupported states where detectable.

## Static secret workflow

- [x] Treat `auth secret set` as the primary flow for API keys, bearer tokens, and basic auth secrets.
- [x] Treat `auth secret clear` as static secret removal.
- [x] Treat `auth secret rotate` as static secret replacement.
- [x] Treat `auth secret validate` as validation where supported.
- [x] Do not print static secret values in normal output.
- [x] Prefer prompt, stdin, or environment-variable input over argv secret values.
- [x] Provide deterministic unsupported state when validation is unavailable.
- [x] Preserve current API key storage behavior.
- [x] Preserve current bearer token storage behavior.
- [x] Preserve current basic auth username/password storage behavior.

## Status and output

- [x] Provide concise human-readable status output.
- [x] Provide machine-readable JSON status output.
- [x] Provide stable machine-readable state names for automation.
- [x] Distinguish `not_required`.
- [x] Distinguish `configured`.
- [x] Distinguish `authenticated`.
- [x] Distinguish `missing_credentials`.
- [x] Distinguish `expired_credentials` where the underlying credential type/storage exposes expiry; current stored OAuth2 refresh-token status has no local expiry signal.
- [x] Distinguish `invalid_credentials` where the underlying credential type/storage exposes invalidity; current stored OAuth2 refresh-token/static-secret status has no local remote-validity signal.
- [x] Distinguish `secret_store_locked`.
- [x] Distinguish `secret_store_uninitialized`.
- [x] Distinguish `unsupported_auth_type`.
- [x] Distinguish `ambiguous_target`.
- [x] Distinguish `ambiguous_credential`.
- [x] Distinguish `ambiguous_account`.
- [x] Include recommended next commands in human-readable output.
- [x] Avoid requiring automation to scrape prose.

## Secret-store lifecycle

- [x] Detect uninitialized secret store before credential operations.
- [x] Detect locked secret store before credential operations.
- [x] Make setup-required state actionable.
- [x] Make unlock-required state actionable.
- [x] Support intentional first-time setup through `toolbox auth setup`.
- [x] Ensure backup/recovery codes are shown clearly when generated.
- [x] Support explicit unlock through `toolbox auth unlock`.
- [x] Support explicit lock through `toolbox auth lock`.
- [x] Support recovery-code display or generation through `toolbox auth recovery codes`.
- [x] Support rewrapping after recovery through `toolbox auth recovery rewrap`.
- [x] Avoid surprise prompts in non-interactive mode.

## Destructive operation safety

- [x] Show package, credential, account/profile, and affected secret labels before destructive changes.
- [x] Require confirmation for interactive destructive operations.
- [x] Provide `--yes` or equivalent for explicit non-interactive destructive operations.
- [x] Never clear all credentials across ambiguous scope by default.
- [x] Keep OAuth2 token logout separate from OAuth2 client configuration deletion.
- [x] Keep account deletion separate from credential deletion.

## Compatibility and migration

- [x] Remove unreleased prototype auth command forms where they conflict with the new model.
- [x] Remove unreleased prototype auth passthrough parsing where first-class subcommands replace it.
- [x] Do not add deprecation messages for unreleased prototype auth commands.
- [x] Do not add old-to-new command mapping behavior for unreleased prototype auth commands.
- [x] Update existing tests and fixtures to exercise the new command model directly.
- [x] Avoid introducing credential-scope migration code unless needed by released storage behavior.

## Tests

- [x] Add tests for package target resolution.
- [x] Add tests for explicit local target resolution.
- [x] Add tests that local directories are not selected implicitly in the new UX.
- [x] Add tests for OAuth2 configure/login/logout/status.
- [x] Add tests for static secret set/clear/rotate/status.
- [x] Add tests for ambiguous credential handling.
- [x] Add tests for ambiguous account handling.
- [x] Add tests for locked secret-store behavior.
- [x] Add tests for uninitialized secret-store behavior.
- [x] Add tests for non-interactive deterministic failures.
- [x] Add tests that secret values are not printed.
- [x] Add tests for JSON output states.
- [x] Add tests that unreleased prototype command forms are not required for the new UX.
