# RFC: Tool Registry, FQN, and Auto-Download

**Status:** Draft
**Author:** toolbox team
**Date:** 2026-03-24

---

## Problem

Today, toolsets are assembled manually. `Builder.AddFromDir()` loads a package from a local directory. `Builder.AddFromArchive()` loads from a pre-built `.toolbox.pkg` file. Both require the caller to already have the package on disk and to wire everything together imperatively.

This is fine for local development and controlled deployments, but it doesn't work for the world we're building toward:

- A harness author writes a toolset that references `zendesk@2.0.1` and `slack@1.2.0`. They expect those packages to just work — fetched, cached, verified, and ready.
- A tool author publishes a package. Other teams discover it, pin a version, and use it without coordinating file paths.
- A platform operator needs reproducible toolset resolution — the same toolset definition produces the same resolved tools on any machine.

We need:
1. A way to uniquely identify any tool across the ecosystem (FQN)
2. A way to discover and fetch packages without pre-staging them (registry + auto-download)
3. A declarative toolset format that lists package refs and bindings, replacing imperative builder calls
4. A version resolution and integrity model that makes this reproducible and safe

---

## Proposal

### 1. Tool Fully-Qualified Name (FQN)

#### Package identity

A package is identified by its **module path** — a git-host-qualified path:

```
github.com/acme-corp/zendesk-tools
gitlab.com/internal/slack-tools
github.com/solidarity-ai/google-workspace
```

This is the package's globally unique identity. It follows the Go module convention: the git host + org + repo path is the module path. No central allocation authority needed — DNS + git hosting provides uniqueness.

#### Version

Versions use semver with a `v` prefix, matching Go and the existing spec:

```
github.com/acme-corp/zendesk-tools@v2.0.1
```

Versions are always exact. No ranges, no floating tags, no `latest`. A toolset pins a specific version and that's what it gets. This is a deliberate constraint — tools are leaf dependencies consumed by toolsets, not libraries composed with other libraries. There is no diamond dependency problem to solve.

#### Tool path within a package

A tool is identified by its resource path name, derived from its entry filename as defined in the tool definition spec:

```
account.tickets.comments.add
channels.messages.send
users.calendars.events.list
clone
```

#### Full FQN

The complete FQN for a tool is:

```
github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.comments.add
```

Format: `{module_path}@{version}/{tool_path}`

This uniquely identifies one tool across the entire ecosystem. It is stable, human-readable, and can be parsed unambiguously:

- Everything before `@` is the module path
- Between `@` and `/` is the version
- Everything after the first `/` following the version is the tool path

#### Short names

In context where the module path is unambiguous (e.g., within a toolset that has already declared its package dependencies), tools can be referenced by short name:

```
zendesk-tools@v2.0.1/account.tickets.comments.add
```

Or when the version is also unambiguous (only one version of the package in the toolset):

```
zendesk-tools/account.tickets.comments.add
```

The existing toolset design doc uses `zendesk@2.0.1/account.tickets.comments.add` — this is the short-name form. Short names are syntactic sugar resolved at toolset compilation time. The canonical form stored in resolved toolsets always uses the full FQN.

#### Non-git sources

The module path format is git-centric by design. For non-git sources:

- **Local development**: Uses `replace` directives (see section 7) — the module path is still the canonical identity, but resolution is redirected to a local directory.
- **Private registries**: Can proxy git repos or serve archives directly. The module path still identifies the package; the registry is the transport, not the identity.
- **Vendored packages**: A toolset can vendor package archives locally. The module path is metadata inside the archive; the toolset's lockfile records both the identity and the local path.

The module path is always the identity. How you obtain the bytes is a separate concern.

---

### 2. Package Registry / Tool Library

#### Design: Built package registry with pluggable sources

The distribution model separates **package identity** (module path) from **package source** (where the built archive is fetched from). A package source is any system that can serve a `.toolbox.pkg` archive and its manifest for a given module path + version. GitHub Releases is the first and primary source, but the abstraction is not GitHub-specific.

```
Package Identity (module path)  →  Package Source (fetches the archive)
                                      ├── GitHub Releases (primary)
                                      ├── GitLab Releases
                                      ├── Generic HTTP (any URL serving the artifacts)
                                      └── Git source (fallback: clone + build)
```

The reasons for built-archive-first distribution:

- **Release artifacts are the natural distribution unit.** `packaging.Pack` already produces a self-contained `.toolbox.pkg` archive with sha256 integrity. Hosting it anywhere is the simplest publish step.
- **No proxy required for basic use.** Clients download archives directly from the source. No intermediate infrastructure needed.
- **WASM binaries work naturally.** Large WASM binaries are awkward in git history but trivial as hosted artifacts. This eliminates the biggest pain point of git-native distribution.
- **Host-agnostic.** Any CI system that can run `toolbox pack` and upload two files can be a package source.

#### Package source interface

A package source implements a simple contract:

```
Given (module_path, version):
  → return (.toolbox.pkg archive bytes, toolbox.pkg.json manifest bytes)
  → or "not found"
```

The resolver tries sources in order until one succeeds. The source is recorded in the lockfile for provenance.

#### GitHub Releases source (primary)

For GitHub-hosted packages, the release artifact convention is:

A release tagged `v2.0.1` has these assets:

```
{name}.toolbox.pkg       # the archive (same format packaging.Pack produces)
toolbox.pkg.json         # external manifest with sha256
```

We provide an official `solidarity-ai/toolbox-pack-action` that automates this:

```yaml
# .github/workflows/release.yml
name: Release Toolbox Package
on:
  push:
    tags: ['v*']
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: solidarity-ai/toolbox-pack-action@v1
        # Reads toolbox.devpkg.json, runs `toolbox pack`,
        # creates a GitHub Release with the .toolbox.pkg archive
        # and toolbox.pkg.json manifest attached.
```

This action:
1. Validates the package against the dist schema
2. Runs `packaging.Pack` to produce the `.toolbox.pkg` archive and external `toolbox.pkg.json` manifest
3. Creates a GitHub Release for the tag
4. Attaches both files as release assets

Package authors add this workflow once. After that, publishing is: `git tag v2.0.1 && git push --tags`. Equivalent actions/pipelines can be built for GitLab CI, Gitea, or any CI system.

#### Git-source fallback

When no built artifacts are available, the client falls back to git-source resolution:

1. Derive the git clone URL from the module path
2. Fetch the tag matching the version
3. Read the package source from the tagged commit
4. Build locally (`packaging.Pack` — compile TS, validate manifest)
5. Cache the result

This fallback ensures the system works with any git host, even without CI. But the recommended path is always: publish built artifacts.

#### Registry proxy (optional, for discovery and stats)

A registry proxy is **optional** infrastructure. It does **not** host package archives itself — it passes through download links to the underlying package source and provides value-added services:

```
TOOLBOX_PROXY=https://proxy.toolbox.dev
```

The proxy provides:

**1. Discovery and search.** A search API over known packages:

```
GET /v1/search?q=zendesk&runtime=typescript-sandbox
GET /v1/packages/github.com/acme-corp/zendesk-tools
GET /v1/packages/github.com/acme-corp/zendesk-tools/versions
GET /v1/packages/github.com/acme-corp/zendesk-tools@v2.0.1
```

**2. Download stats and popularity.** Tracks download counts per package/version, enabling ecosystem visibility (most-used packages, trending tools).

**3. Availability caching (optional).** Can optionally mirror archives for resilience against source outages or deletions. This is a caching layer, not the primary host.

The proxy is not required for fetching packages. Clients download directly from the package source (e.g., GitHub Releases API). The proxy adds value for ecosystem-wide discovery, stats, and organizational visibility.

#### Proxy protocol

When present, the proxy serves metadata and can redirect or cache archives:

```
GET /v1/packages/{module_path}/@v/{version}.info     → version metadata (JSON, includes source download URLs)
GET /v1/packages/{module_path}/@v/{version}.pkg      → .toolbox.pkg archive (cached or redirect to source)
GET /v1/packages/{module_path}/@v/{version}.manifest  → toolbox.pkg.json (cached or redirect to source)
GET /v1/packages/{module_path}/@latest                → latest version info
GET /v1/packages/{module_path}/@v/list                → available versions
```

The `.info` endpoint returns source URLs so clients can fetch directly from the package source. The `.pkg` and `.manifest` endpoints can either serve cached copies or redirect (302) to the source URL.

#### Package metadata

Each package version serves this metadata (from the proxy's `.info` endpoint, or derived from git):

```json
{
  "module": "github.com/acme-corp/zendesk-tools",
  "version": "v2.0.1",
  "name": "zendesk-tools",
  "runtime": "typescript-sandbox",
  "tools": [
    {
      "path": "account.tickets.list",
      "description": "List tickets for an account",
      "accessMode": "readOnly"
    },
    {
      "path": "account.tickets.comments.add",
      "description": "Add a comment to a ticket",
      "accessMode": "appendOnly"
    }
  ],
  "published": "2026-03-15T10:30:00Z",
  "sha256": "a1b2c3..."
}
```

This is enough for discovery (search results can show tool names, descriptions, and safety metadata) without downloading the full archive.

---

### 3. Auto-Downloading

#### Resolution flow

When a toolset references a package, resolution follows this order:

```
1. Check replace directives (local dev overrides)
2. Check local cache (content-addressable store)
3. Fetch from proxy (if TOOLBOX_PROXY is set)
4. Fetch from GitHub Release (download .toolbox.pkg + manifest from release assets)
5. Fetch from git source (clone at tag, build locally with packaging.Pack)
6. Store in local cache
```

Steps 4 and 5 are the two remote fetch strategies. GitHub Releases (step 4) is preferred because it downloads a pre-built archive — fast and no local build step. Git-source (step 5) is the fallback when release artifacts don't exist.

#### Local cache

The local cache is content-addressable, keyed by module path + version + archive sha256:

```
~/.cache/toolbox/
  pkg/
    github.com/acme-corp/zendesk-tools/
      @v/
        v2.0.1.manifest    # toolbox.pkg.json with sha256
        v2.0.1.pkg          # .toolbox.pkg archive
        v2.0.1.info         # version metadata
    github.com/solidarity-ai/google-workspace/
      @v/
        v1.0.0.manifest
        v1.0.0.pkg
        v1.0.0.info
```

This layout mirrors the proxy protocol, so the cache can be populated either from a proxy response or from a local build. When loaded, `packaging.LoadArchive` is called with the cached `.pkg` and `.manifest` paths — the existing integrity verification (sha256 check, internal/external manifest comparison) applies unchanged.

#### Integrity verification

Every cached archive is verified on load using the existing `LoadArchive` flow:

1. External manifest contains sha256 of the archive
2. `LoadArchive` computes sha256 of the archive bytes and compares
3. Internal manifest inside the archive must match the external manifest
4. Dist-mode validation is applied

The lockfile (see section 4) records the expected sha256 for each package version. If the cached archive doesn't match the lockfile hash, the cache entry is treated as corrupt and re-fetched.

#### Interaction with existing `packaging.LoadArchive`

Auto-download is a layer above `packaging.LoadArchive`, not a replacement. The flow is:

```
resolve(module_path, version)
  → locate or download archive + manifest to cache
  → packaging.LoadArchive(cachePath, manifestPath)
  → returns LoadedPackage (same type the Builder already consumes)
```

The `Builder` gains a new method alongside the existing two:

```go
// AddFromRegistry resolves and downloads a package by module path and version.
func (b *Builder) AddFromRegistry(modulePath, version string) error
```

Internally, this calls the resolver, which calls `LoadArchive` on the cached result.

---

### 4. Toolset Composition — Declarative Format

#### Toolset file: `toolbox.toolset.json`

A toolset is declared as a JSON file that lists package dependencies and bindings:

```json
{
  "packages": {
    "github.com/acme-corp/zendesk-tools": "v2.0.1",
    "github.com/solidarity-ai/slack-tools": "v1.2.0"
  },

  "replace": {
    "github.com/acme-corp/zendesk-tools": "../zendesk-tools"
  },

  "context": {
    "customer_id": { "description": "The customer this toolset is scoped to" },
    "environment": { "description": "production or staging" },
    "allowed_channels": { "description": "Slack channels the agent may post to" }
  },

  "credentials": {
    "slack_token": { "source": "env:SLACK_BOT_TOKEN" },
    "zendesk_key": { "source": "vault:zendesk/api-key" }
  },

  "resource_bindings": {
    "github.com/acme-corp/zendesk-tools": {
      "account_id": { "value": "context.customer_id", "hidden": true }
    }
  },

  "tools": [
    {
      "tool": "github.com/solidarity-ai/slack-tools@v1.2.0/channels.messages.send",
      "bindings": {
        "channel_id": {
          "value": "params.channel_id",
          "hidden": false,
          "check": "params.channel_id in context.allowed_channels"
        },
        "message": { "value": "params.message" }
      }
    },
    {
      "tool": "github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.get",
      "bindings": {
        "ticket_id": { "value": "params.ticket_id" }
      }
    },
    {
      "tool": "github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.comments.add",
      "bindings": {
        "ticket_id": { "value": "params.ticket_id" },
        "body": { "value": "params.body" }
      }
    },
    {
      "tool": "github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.list"
    }
  ]
}
```

Key design choices:

- **`packages`** declares all dependencies with exact versions. This is the source of truth for what packages the toolset uses. Supports aliasing (see below).
- **`replace`** redirects a module path to a local directory (for development). Semantics match Go's `replace` directive.
- **`tools`** lists the specific tools included in the toolset with their bindings. Tool references use the FQN (or short name resolvable from the `packages` map).
- **`resource_bindings`** are scoped per package (module path). This resolves the open question in the toolset design doc — "resource-level bindings may still need package scoping." They do. Different packages may infer `account_id` with different semantics.
- **`context`** declares the expected context inputs. These are documentation and validation — the harness provides actual values at runtime.
- **`credentials`** binds package-declared credential requirements to sources. Packages declare what credentials they need (e.g., `slack_token`, `zendesk_key`) in their manifest. The toolset binds those to concrete sources — environment variables (`env:SLACK_BOT_TOKEN`), secret vaults (`vault:zendesk/api-key`), or other providers. The package declares *what* it needs; the toolset decides *where* the value comes from.

#### Package aliasing for name conflicts

Two packages from different organizations may share the same `name` field (e.g., both called `"zendesk-tools"`). Since short names are derived from the package `name`, this creates ambiguity. The toolset resolves this with explicit aliases, similar to Go's import renaming:

```json
{
  "packages": {
    "github.com/acme-corp/zendesk-tools": "v2.0.1",
    "github.com/other-org/zendesk-tools": { "version": "v1.0.0", "alias": "other-zendesk" }
  }
}
```

When no alias is specified, the short name defaults to the package `name` field from the manifest. When an alias is set, the alias replaces the package name for all short-name resolution within this toolset.

Rules:
- If two packages resolve to the same short name and neither has an alias, the toolset fails to parse with a clear error: *"packages 'github.com/acme-corp/zendesk-tools' and 'github.com/other-org/zendesk-tools' both resolve to short name 'zendesk-tools'; add an alias to one"*.
- Aliases are toolset-local — they don't change the package's canonical identity (module path) or its FQN.
- Full FQNs always work regardless of aliasing. Aliases only affect short-name references.
- The alias appears in `AgentView` tool names when the package prefix is shown (e.g., `other-zendesk/tickets.list` vs `zendesk-tools/tickets.list`).

#### Lockfile: `toolbox.toolset.lock`

The lockfile records the resolved state of all packages:

```json
{
  "packages": {
    "github.com/acme-corp/zendesk-tools@v2.0.1": {
      "archive_sha256": "a1b2c3d4e5f6...",
      "manifest_sha256": "9f8e7d6c5b4a...",
      "git_sha": "abc123def456789...",
      "resolved_from": "github-release",
      "resolved_at": "2026-03-15T10:30:00Z"
    },
    "github.com/solidarity-ai/slack-tools@v1.2.0": {
      "archive_sha256": "f6e5d4c3b2a1...",
      "manifest_sha256": "1a2b3c4d5e6f...",
      "git_sha": "def789abc123456...",
      "resolved_from": "git-source",
      "resolved_at": "2026-03-20T08:15:00Z"
    }
  }
}
```

Each lockfile entry records three integrity hashes:

- **`archive_sha256`**: SHA256 of the `.toolbox.pkg` archive bytes. Verified by `packaging.LoadArchive` on every load.
- **`manifest_sha256`**: SHA256 of the external `toolbox.pkg.json` manifest. Ensures the manifest hasn't been tampered with independently of the archive.
- **`git_sha`**: The git commit SHA for the version tag. Pins the exact source commit for auditing and reproducibility. If the git host force-pushes the tag to a different commit, this detects the change.

The lockfile is committed to version control. It guarantees:

- **Reproducibility**: Same lockfile + same toolset file = same resolved packages on any machine.
- **Integrity**: All three hashes are verified on load. Mismatch on any hash means corruption or tampering — the cache entry is discarded and re-fetched. If the re-fetched version also doesn't match, resolution fails with an error.
- **Auditability**: `resolved_from`, `resolved_at`, and `git_sha` provide full provenance. You can trace any package in a resolved toolset back to the exact source commit.

Running `toolbox resolve` reads the toolset file, resolves all packages, and writes/updates the lockfile. Running `toolbox resolve --upgrade github.com/acme-corp/zendesk-tools` bumps one package to its latest version and updates the lockfile.

#### Two interfaces: declarative file and programmatic Builder

Toolsets can be assembled two ways. Both are first-class — `toolbox.toolset.json` is not required.

**Declarative file** — for config-driven toolsets:

```go
ts, err := toolset.Load("toolbox.toolset.json")  // reads toolset + lockfile
resolved, err := ts.Resolve(ctx)                   // auto-downloads, caches, resolves
```

**Programmatic Builder** — for toolsets assembled in code (harnesses, tests, dynamic composition):

```go
b := toolset.New()
b.AddFromRegistry("github.com/acme-corp/zendesk-tools", "v2.0.1")
b.AddFromRegistry("github.com/solidarity-ai/slack-tools", "v1.2.0")
b.AddFromDir("../local-tools")  // local dev package
// bindings, context, credentials configured programmatically
resolved := b.Resolve()
```

The declarative file is a convenience that calls the same Builder APIs underneath. Harness authors who build toolsets programmatically get the same auto-download, caching, and integrity verification — the file format is just one way to drive it.

---

### 5. Version Resolution

#### Exact pins only

Toolset files pin exact versions. There are no version ranges, no `^`, no `~`, no `>=`.

```json
{
  "packages": {
    "github.com/acme-corp/zendesk-tools": "v2.0.1"
  }
}
```

This means `v2.0.1`, not "any v2.x.x". Period.

#### Why no ranges

Tools are leaf dependencies. A toolset consumes tools; tools don't consume other tools. There is no transitive dependency graph. There is no version resolution algorithm needed because there is nothing to resolve — each package appears at most once in a toolset, at exactly one version.

If two toolsets reference different versions of the same package, they are two different toolsets. If someone tries to compose them, the versions must agree or the composition fails. This is intentional.

#### Version conflicts

If a toolset lists the same module path twice with different versions, that is a static error caught at parse time.

If a higher-level composition system (e.g., toolset inheritance or merging) produces a conflict, the resolution is: error. The operator must pick one version. We do not attempt to load two versions of the same package simultaneously — the complexity of dual-loading with potentially conflicting tool names and resource bindings is not worth the marginal flexibility.

#### Upgrade workflow

```bash
# Show available versions for a package
toolbox versions github.com/acme-corp/zendesk-tools

# Upgrade one package to latest
toolbox resolve --upgrade github.com/acme-corp/zendesk-tools

# Upgrade all packages to latest
toolbox resolve --upgrade-all

# Upgrade to a specific version
toolbox resolve --set github.com/acme-corp/zendesk-tools=v3.0.0
```

Each of these updates both the toolset file and the lockfile.

---

### 6. Security Model

#### Archive integrity

Already implemented: `packaging.LoadArchive` verifies sha256 of the archive against the external manifest. The lockfile adds a second layer — the expected sha256 is recorded at resolve time and verified on every load.

The integrity chain is:

```
lockfile sha256  →  external manifest sha256  →  archive bytes
                    external manifest          ←→  internal manifest (must match)
                    dist-mode validation       ←   compiled package
```

#### Package signing (future)

The initial implementation relies on sha256 integrity and transport security (HTTPS for git and proxy). Package signing is a natural extension:

- The proxy (or git tag) can include a detached signature alongside the archive
- The lockfile records the expected signature
- Verification uses a configurable trust store (list of trusted public keys)

This is deferred to a future RFC. Sha256 + HTTPS + lockfile provides a solid baseline. Signing adds defense against compromised proxies or git hosts.

#### Trust model

Trust is scoped, not global:

1. **Git host trust**: You trust packages from git hosts you can access. Private repos require authentication; public repos are inherently lower trust.
2. **Proxy trust**: If you configure a proxy, you trust it to serve authentic archives. The sha256 in the lockfile protects against proxy tampering after initial resolution.
3. **Lockfile trust**: The lockfile is committed to your repo. You trust your own version control. Any change to lockfile hashes is visible in code review.
4. **Org-scoped trust** (future): An organization can maintain an allowlist of approved module paths. The resolver refuses to fetch packages not on the list.

#### Capability declarations (future)

Packages can declare what capabilities they need:

```json
{
  "name": "zendesk-tools",
  "capabilities": {
    "network": ["*.zendesk.com"],
    "exec": ["gwc.wasm"]
  }
}
```

This is informational today — the runtime sandbox already constrains what tools can do (fetch goes through Go's proxy, exec runs in WASIX sandbox). But declaring capabilities explicitly enables:

- Toolset authors to audit what a package claims to need before adding it
- Future enforcement at the proxy level (reject packages that declare capabilities beyond a policy)
- Registry search filtering ("show me read-only packages that only talk to Zendesk")

Deferred to a future RFC. The sandbox is the enforcement layer; capability declarations are the transparency layer.

---

### 7. Local Development Workflow

#### `replace` directives

A toolset file can redirect any module path to a local directory:

```json
{
  "packages": {
    "github.com/acme-corp/zendesk-tools": "v2.0.1"
  },
  "replace": {
    "github.com/acme-corp/zendesk-tools": "../zendesk-tools"
  }
}
```

When a `replace` is present for a module path, the resolver:

1. Ignores the version in `packages` for that module
2. Calls `packaging.LoadDev(replacePath)` instead of downloading
3. Uses dev-mode validation (lenient, warnings instead of errors)
4. Does NOT update the lockfile entry for that package

This matches Go's `replace` semantics exactly. The `replace` block is typically not committed — it's either in a gitignored overlay file or added temporarily during development.

#### Relationship to `toolbox.devpkg.json`

The existing `toolbox.devpkg.json` is the authoring format for a package under development. It defines the package from the package author's perspective.

`toolbox.toolset.json` is the consumption format. It defines which packages a harness uses and how they're bound.

The relationship:

```
Package author's repo:         Harness author's repo:
  toolbox.devpkg.json            toolbox.toolset.json
  tools/                         toolbox.toolset.lock
    tickets.list.ts
    tickets.get.ts

          ↓ (pack/publish)              ↓ (resolve)
      .toolbox.pkg              resolved toolset with
      toolbox.pkg.json          all packages loaded
```

A tool author works on their package using `toolbox.devpkg.json`. They test by running tools locally. When ready, they tag a version in git. The GitHub Action (see section 2) automatically packs and publishes the release artifact.

A harness author references that package in `toolbox.toolset.json`. During development, they use `replace` to point at a local checkout. For production, the `replace` is removed and the lockfile pins the published version.

#### Dev server

For a fast local iteration loop:

```bash
# In the tool author's repo
toolbox dev

# Watches toolbox.devpkg.json and tools/ for changes
# Recompiles TS on change
# Serves the package locally for toolset testing
```

```bash
# In the harness author's repo (with replace pointing to local package)
toolbox serve toolbox.toolset.json

# Resolves the toolset (using replace for local packages)
# Watches for changes in replaced packages
# Hot-reloads when tool source changes
```

---

### 8. Interaction with Bindings

#### FQNs in binding references

Tool references in the `tools` array of a toolset use FQNs:

```json
{
  "tool": "github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.comments.add",
  "bindings": { ... }
}
```

The version in the tool reference must match the version in `packages`. If it doesn't, that's a static error. This redundancy is intentional — it makes each tool reference self-describing and greppable.

Short names are allowed when unambiguous:

```json
{
  "tool": "zendesk-tools/account.tickets.comments.add",
  "bindings": { ... }
}
```

The resolver expands this to the full FQN using the `packages` map.

#### Resource bindings are package-scoped

As proposed in the toolset file format, resource bindings are scoped to a module path:

```json
{
  "resource_bindings": {
    "github.com/acme-corp/zendesk-tools": {
      "account_id": { "value": "context.customer_id", "hidden": true }
    }
  }
}
```

This binding applies to all tools from `github.com/acme-corp/zendesk-tools` whose resource path starts with `account`. It does NOT apply to a hypothetical `account_id` in a different package.

This resolves the open question from the toolset design doc. Different packages may infer the same resource param name (e.g., `account_id`) with completely different semantics. Package-scoping prevents cross-contamination.

#### Version changes and binding stability

When a package version is bumped (e.g., `v2.0.1` to `v2.1.0`):

- **Minor version bump** (additive only per the spec): Existing tool paths are stable. Existing bindings continue to work. New tools may appear but have no bindings — the harness author adds them explicitly if desired.
- **Major version bump** (breaking): Tool paths, params, or resource structure may change. Bindings may break. The harness author must review and update bindings. This is the expected cost of a major version bump.

The lockfile makes this safe: bindings are written against a specific version, and the lockfile pins that version. Nothing changes until the harness author explicitly runs `toolbox resolve --upgrade`.

#### Resolved toolset stores canonical FQNs

After resolution, every `BoundTool` in the resolved toolset carries the full FQN:

```
github.com/acme-corp/zendesk-tools@v2.0.1/account.tickets.comments.add
```

Not the short name. Not a relative path. The full canonical FQN. This makes resolved toolsets self-describing and portable — you can look at a resolved toolset and know exactly which package, version, and tool every binding refers to.

The agent never sees FQNs. The agent sees the shortened names from `AgentView` (e.g., `tickets.comments.add`). FQNs are infrastructure plumbing.

---

## Alternatives Considered

### Central registry (npm model)

A central registry where packages are published with `toolbox publish` and fetched with `toolbox install`.

**Rejected because:**
- Requires standing up and operating registry infrastructure before anyone can publish
- Creates a single point of failure and trust
- Packages would need separate identity from their git repos (npm-style package names vs. git URLs)
- The Go ecosystem proved that git-native package distribution works well and scales

GitHub Releases are the middle ground — packages exist in git repos, but distribution uses pre-built release artifacts rather than requiring clients to clone and build. The optional proxy adds discoverability and cross-host caching on top.

### OCI registries

Store packages as OCI artifacts in container registries (like ORAS).

**Rejected as primary mechanism because:**
- OCI registries are ubiquitous for containers but overkill for small TS+WASM packages
- Adds dependency on OCI tooling and registry infrastructure
- Package identity would still need to be defined separately from the OCI reference

However, an OCI-backed proxy could be a reasonable implementation choice for the proxy layer. The package identity (module path) stays git-based; the proxy happens to store archives in OCI. This is an implementation detail of the proxy, not a design choice for the package model.

### Version ranges

Allow `>=v2.0.0, <v3.0.0` or `^v2.0.1` in toolset files.

**Rejected because:**
- Tools are leaf dependencies, not libraries. There is no dependency graph to resolve.
- Ranges introduce non-determinism — the same toolset file can resolve to different versions depending on when you resolve.
- Lockfiles mitigate this, but then the range is just a hint that gets pinned immediately.
- Exact pins are simpler, more predictable, and sufficient for the use case.

### Toolset inheritance / composition

Allow toolsets to extend other toolsets (`"extends": "base-toolset.json"`).

**Deferred, not rejected.** This is a real need (e.g., a base toolset for "customer support" that teams customize), but it introduces version conflict resolution, binding override semantics, and merge ordering questions. Worth a separate RFC once the base toolset format is proven.

### Content-addressable identity (like Nix)

Identify packages by the hash of their contents rather than by module path + version.

**Rejected because:**
- Loses human readability — `sha256:a1b2c3...` is not greppable or meaningful
- Module path + version already provides a stable, human-readable identity
- Content-addressable hashing is still used for integrity (sha256 in manifests and lockfiles), just not as the primary identity

---

## Open Questions

### 1. Proxy authentication

How does the proxy authenticate clients? Options:
- API keys per organization
- OAuth / OIDC integration
- Git credential forwarding (use existing git auth)
- Mutual TLS

Likely answer: start with git credential forwarding for git-direct fetches and API keys for proxy access. Align with whatever auth the broader platform uses.

### 2. Package namespacing beyond git path

Should there be a shorter namespace for "official" or "well-known" packages? E.g., `toolbox.dev/zendesk` instead of `github.com/solidarity-ai/zendesk-tools`.

Tempting but premature. Go avoided this (there is no `go.dev/http`) and it worked out fine. Custom import paths (like Go's vanity imports) could provide this later without changing the underlying model.

### 3. WASM binary distribution

Largely resolved by the GitHub Releases model. WASM binaries are included in the `.toolbox.pkg` archive produced by `toolbox pack`. The archive is attached as a release asset — clients download the archive, not the git history. The git repo may store WASM sources or use Git LFS, but consumers never interact with that.

For the git-source fallback path, WASM binaries must be present in the repo at the tagged commit (either committed directly or via LFS). This is another reason to prefer the release artifact path.

### 4. Toolset file format — JSON vs. something else

JSON is verbose for a config file. Alternatives: TOML, YAML, CUE, Jsonnet.

Proposal: stay with JSON for now. The toolset file is machine-read more often than human-edited, JSON schema validation is well-supported, and we already use JSON for manifests. If verbosity becomes a real pain point, TOML is the natural upgrade (like Go's migration from JSON to go.mod's custom format).

### 5. Multi-package repos (monorepos)

Can a single git repo contain multiple packages? E.g., `github.com/acme-corp/tools/zendesk` and `github.com/acme-corp/tools/slack` in the same repo.

Probably yes, following Go's sub-module convention — the module path includes the subdirectory, and the version tag is prefixed (`zendesk/v2.0.1`). But this adds complexity to the git-based resolution logic. Worth supporting but not in the first iteration.

### 6. Deprecation and yanking

How does a package author signal that a version should not be used? The proxy could support a yank/deprecation flag. The resolver would warn (or error) on yanked versions. Git tags can't be "deprecated" natively, so this requires proxy support.

### 7. Replace overlay file

Should `replace` directives live in the toolset file directly, or in a separate gitignored file (e.g., `toolbox.toolset.local.json`) that's merged at load time?

Leaning toward a separate overlay file. Go puts `replace` in `go.mod` which means it either gets committed (bad for CI) or developers have to remember not to commit it. A separate file that's gitignored by default is cleaner.

---

## Implementation Sequence

This RFC covers a large surface area. The recommended build order:

1. **FQN types and parsing** — Add `ModulePath`, `Version`, `ToolFQN` types to the `tool` package. Parse and format FQNs. No behavioral changes.

2. **Local cache layout** — Implement the `~/.cache/toolbox/pkg/` structure. Write cached archives after `Pack`, read them in a new `LoadFromCache` path.

3. **GitHub Release resolver** — Given a module path + version, fetch the `.toolbox.pkg` archive and manifest from the GitHub Release. Store in the cache. This is the primary remote fetch path.

4. **Git-source fallback resolver** — Given a module path + version, clone/fetch the git repo at the tag, run `Pack` to produce an archive, store in the cache. Used when release artifacts aren't available.

5. **`Builder.AddFromRegistry`** — New method that calls the resolver (release then git-source), then `LoadArchive` on the cached result. Toolsets can now reference packages by module path + version.

6. **GitHub Action for packing** — `solidarity-ai/toolbox-pack-action` that runs `toolbox pack` and attaches artifacts to a GitHub Release on tag push.

7. **Toolset file format** — Parse `toolbox.toolset.json`, resolve all packages, produce a `ResolvedToolset`. Lockfile generation.

8. **Replace directives** — Support `replace` in the toolset file (or overlay) for local development.

9. **Proxy protocol** — Implement the proxy server and client. Add `TOOLBOX_PROXY` support to the resolver.

10. **Search and discovery** — Proxy search API, `toolbox search` CLI command.

Steps 1-5 are the critical path. Step 6 (GH Action) can be built in parallel. Steps 7-10 can be parallelized after the core resolver works.
