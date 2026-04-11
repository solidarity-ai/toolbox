# RFC: Auth CLI Changes

## Status

Draft.

This RFC scopes the `toolbox auth ...` redesign separately from the broader CLI
move to Kong. The non-auth CLI can move first; auth has distinct workflow and
security constraints that need an explicit design.

## Context

The current `toolbox auth` surface is a single command with mutually exclusive
flags and a package-directory-oriented target model. That shape is no longer a
good fit for the product direction:

- the rest of the CLI is moving to a more structured, package-manager-like UX
- auth needs to operate on installed toolset targets, not just package dirs
- OAuth and non-OAuth credentials have different lifecycle semantics
- credential namespaces must remain isolated across installed packages, local
  dirs, and overridden packages

The goal of this RFC is to capture the auth-specific constraints and the
currently agreed direction so the remaining design work happens in one place.

## Confirmed Decisions

### 1. Auth remains a separate design track

The first Kong migration may keep the legacy auth behavior behind the new root
parser. The public auth redesign should be completed separately rather than
forcing it into the first non-auth CLI pass.

### 2. OAuth and non-OAuth workflows are different

The auth CLI should not pretend all credentials share one lifecycle.

- `login` / `logout` are OAuth-only
- non-token credential material belongs under a separate `secret` surface
- `status` should report both OAuth and non-OAuth state together

### 3. Static OAuth secrets are handled as secrets

If an OAuth credential also needs static inputs such as `client_id` or
`client_secret`, those are managed by the secret workflow, not by the token
login/logout workflow.

In other words:

- `auth login` / `auth logout` manage token lifecycle
- `auth secret set` / `auth secret clear` manage static secret material

### 4. Target classes must be explicit

Auth cannot silently treat one positional target as both an installed package
and a local package dir. That would blur a real security boundary.

There are two target classes:

- installed target: resolved from a toolset context
- local-dir target: explicitly selected with `--dir PATH`

The CLI must expose that distinction rather than hiding it in fallback logic.

### 5. Credential namespaces must remain isolated

Credential storage must distinguish:

- an installed/module target
- an overridden target selected through toolset replacement/override behavior
- an explicit local-dir target selected with `--dir`

If a package is overridden, auth material should use the override namespace, not
the original module namespace. If `--dir PATH` is used, auth material should use
the local-dir namespace, also separate from the installed/module namespace.

## Working Direction

The leading candidate public tree is:

```text
toolbox auth status [<target>] [--toolset FILE] [--dir PATH]
toolbox auth login <target> [--toolset FILE] [--credential NAME] [--account NAME]
toolbox auth logout <target> [--toolset FILE] [--credential NAME] [--account NAME]
toolbox auth secret set <target> [--toolset FILE] [--dir PATH] [--credential NAME] [--account NAME]
toolbox auth secret clear <target> [--toolset FILE] [--dir PATH] [--credential NAME] [--account NAME]
toolbox auth account rename <target> <old> <new> [--toolset FILE] [--credential NAME]
toolbox auth account delete <target> <account> [--toolset FILE] [--credential NAME]
```

With the following behavior:

- `status` is the only auth command allowed to omit `<target>`
- omitted target on `status` means "show all auth-relevant packages from the
  selected/default toolset"
- mutating auth operations always require an explicit target
- `login` / `logout` only apply to OAuth credentials
- `secret set` / `secret clear` apply to non-token secret material, including
  static OAuth inputs

## Open Questions

These remain intentionally unresolved and should be finished before the auth
CLI itself is rewritten:

1. Installed target resolution details

- exact matching rules for `<target>` inside a toolset
- whether package `name`, module path, aliases, or toolset-local labels are
  valid selectors
- whether any auth command should infer a single credential without
  `--credential`

2. Local-dir auth UX

- whether `--dir PATH` is valid on all non-token commands or only a subset
- how help text should explain the installed-target vs local-dir boundary
- whether `--dir` should require an explicit `--credential`

3. OAuth logout semantics

- whether logout clears only token material or any account-linked OAuth data
- how logout should behave when a credential has multiple accounts

4. Secret set/clear interaction model

- whether `secret set` prompts field-by-field based on credential type
- whether structured inputs should ever be accepted via flags
- how secret clearing scopes to shared vs per-account values

5. Status output shape

- exact formatting of human output
- whether `--json` should ship with the first auth redesign pass
- how to present overridden and local-dir namespaces clearly

## Non-Goals

This RFC does not lock:

- the full implementation of auth target resolution
- the final storage schema for local-dir credential namespaces
- migration behavior from the current legacy auth flags to the new auth tree

Those should be addressed in the follow-up auth CLI implementation RFC or
implementation plan.
