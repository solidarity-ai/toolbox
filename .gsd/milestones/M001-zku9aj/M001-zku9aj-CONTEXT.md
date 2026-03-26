# M001-zku9aj: Tool Registry, FQN, and Auto-Download — Context

**Gathered:** 2026-03-26
**Status:** Ready for planning

## Project Description

Toolbox is a platform for giving agents better tools. This milestone adds the package registry and resolution layer — the plumbing that lets harness authors reference packages by module path + version in a toolset file, and have them automatically fetched from GitHub Releases (or git-source fallback), cached locally with sha256 integrity, and loaded into the existing Builder → ResolvedToolset pipeline.

## Why This Milestone

Today, toolsets are assembled manually. `Builder.AddFromDir()` loads from a local directory, `Builder.AddFromArchive()` from a pre-built archive. Both require the package to already be on disk. There's no way to say "give me zendesk-tools@v2.0.1" and have it resolved, fetched, cached, and loaded automatically.

This milestone is the bridge from local-only package loading to a real distribution model.

## User-Visible Outcome

### When this milestone is complete, the user can:

- Write a `toolbox.toolset.json` declaring package dependencies by module path + version
- Run `toolbox resolve` and have all packages fetched (from GitHub Releases or git-source), cached, integrity-verified, and ready
- Run `toolbox versions` to see available versions for a package
- Run `toolbox resolve --upgrade` to bump package versions
- Use `replace` directives in `toolbox.toolset.local.json` for local dev
- Pin untagged commits via pseudo-versions

### Entry point / environment

- Entry point: `toolbox` CLI commands (`resolve`, `versions`) and Go library API (`Builder.AddFromRegistry`, `toolset.Load`)
- Environment: local dev
- Live dependencies involved: GitHub API (for release asset fetching), git (for git-source fallback)

## Completion Class

- Contract complete means: All resolver paths (GitHub Releases, git-source, pseudo-version) produce correct cached archives verified by sha256. Toolset files parse and resolve. Lockfiles are generated and verified.
- Integration complete means: The full pipeline works end-to-end — toolset file → resolve → cache → Builder → ResolvedToolset with real loaded tools.
- Operational complete means: CLI commands work against emulate in CI. Local cache persists across runs. Replace directives redirect correctly.

## Final Integrated Acceptance

To call this milestone complete, we must prove:

- `toolbox resolve -f demo.toolset.json` fetches packages from emulate, writes a lockfile, and subsequent resolves are cache hits
- `toolbox resolve --upgrade` bumps a package version and updates the lockfile
- A toolset with a `replace` directive loads from the local dir instead of fetching
- The Builder produces a working ResolvedToolset with tools from registry-resolved packages
- All tests pass in CI using emulate (no real GitHub calls)

## Risks and Unknowns

- **Emulate from Go tests** — vercel-labs/emulate is a Node.js server. Running it from Go tests (start as subprocess, seed via HTTP, tear down) is unproven. If it doesn't work reliably in CI, all resolver tests need a different strategy. This is the highest-risk item.
- **Git operations in tests** — Git-source fallback requires creating actual git repos in temp dirs. Git operations in CI can be flaky (permissions, git config). Need to verify this works in GitHub Actions.
- **Pseudo-version timestamp parsing** — Go modules use a specific pseudo-version format. Need to match it exactly or interop breaks.
- **PackageSource interface shape** — The abstraction that unifies GitHub Releases and git-source needs to be right on first try. Getting it wrong means rework in the composition layer.

## Existing Codebase / Prior Art

- `tool/tool.go` — `Package`, `ResolvedTool`, `AccessMode` types (62 lines). FQN types will live here.
- `toolset/toolset.go` — `Builder` with `AddFromDir`, `AddFromArchive`, `Resolve`. `AddFromRegistry` will be added here.
- `packaging/packaging.go` — Facade: `Pack`, `LoadDev`, `LoadArchive`. The resolver will call `LoadArchive` on cached results.
- `packaging/internal/archive/archive.go` — Archive creation and loading with sha256 verification, tar+zstd, in-memory FS.
- `packaging/internal/manifest/manifest.go` — Dev/pkg manifest parsing, compilation, validation.
- `packaging/internal/source/source.go` — `LoadedPackage`, `LoadDir`, `ResolvedTools()`. The registry produces the same `LoadedPackage` type.
- `registry/README.md` — Placeholder describing the registry package's intended role.
- `cmd/toolbox-pack/main.go` — Existing CLI pattern for the `toolbox-pack` command.
- `testutil/fixtures/` — Existing test fixture infrastructure.

> See `.gsd/DECISIONS.md` for all architectural and pattern decisions — it is an append-only register; read it during planning, append to it during execution.

## Design Authority

**`docs/rfc-tool-registry.md` is the authoritative design document for this milestone.** All implementation decisions, open questions, alternative analysis, and detailed specifications live there. When in doubt about design intent, read the RFC. When researching implementation approaches, reference the specific RFC section.

Key RFC sections:
- §1: FQN spec (module path, version, tool path, short names)
- §2: Package registry / tool library (GitHub Releases source, git-source fallback, proxy)
- §3: Auto-downloading (resolution flow, local cache, integrity verification)
- §4: Toolset composition (declarative format, lockfile, pseudo-versions)
- §5: Version resolution (exact pins, upgrade workflow)
- §6: Security model (archive integrity, trust model)
- §7: Local development workflow (replace directives)
- §8: Interaction with bindings (FQNs in binding references)
- Implementation sequence section at the end

## Relevant Requirements

- R001–R011 are all owned by this milestone (see `.gsd/REQUIREMENTS.md`)
- R001 (FQN) is the identity foundation everything else builds on
- R011 (emulate test infrastructure) is the highest-risk item — must be proven first

## Scope

### In Scope

- FQN types (ModulePath, Version, ToolFQN) in the `tool` package
- Local cache at `~/.cache/toolbox/pkg/` with proxy-mirroring layout
- GitHub Releases resolver with `GITHUB_BASE_URL` override
- Git-source fallback resolver (clone at tag → Pack → cache)
- Pseudo-version parsing and resolution
- `Builder.AddFromRegistry` wiring into existing LoadArchive
- Minimal toolset file format (packages map + tools list)
- Lockfile generation and verification
- Replace directives via `toolbox.toolset.local.json`
- CLI: `toolbox resolve`, `toolbox versions`, basic upgrade
- Shared Go test fixture using vercel-labs/emulate
- Failure scenario test builders (corrupt archives, mismatched hashes, etc.)

### Out of Scope / Non-Goals

- Proxy server (deferred to separate milestone)
- Search/discovery API (deferred)
- Package aliasing for name conflicts (deferred)
- Toolset bindings, credentials, context sections (deferred)
- GitHub Action for pack/publish (deferred, separate repo)
- Package signing (future RFC)
- Capability declarations (future RFC)
- Toolset inheritance/composition (future RFC)
- Multi-package monorepos (future RFC)

## Technical Constraints

- Must use existing `packaging.LoadArchive` for archive verification — don't reinvent the sha256 chain
- Must produce `source.LoadedPackage` (same type Builder already consumes) — no new package abstraction
- FQN types must live in the `tool` package alongside `Package` and `ResolvedTool`
- Registry code lives in the `registry/` package (placeholder already exists)
- Toolset file parsing should extend the existing `toolset` package, not create a new one
- CLI should follow the pattern established by `cmd/toolbox-pack/` — separate module in `cmd/`
- Test infrastructure uses vercel-labs/emulate started via Go `exec.Command`, not mocked HTTP

## Integration Points

- **GitHub REST API** — Release by tag endpoint, release asset download. Must support base URL override.
- **Git protocol** — Clone/fetch for git-source fallback. Uses local file:// URLs in tests.
- **packaging.Pack** — Called by git-source resolver to build archives from cloned source.
- **packaging.LoadArchive** — Called by all resolver paths to verify and load cached archives.
- **toolset.Builder** — Gains `AddFromRegistry` method that chains resolver → LoadArchive.
- **vercel-labs/emulate** — Started as subprocess in tests, seeded via GitHub REST API.

## Open Questions

- **Cache directory override** — Should we support `TOOLBOX_CACHE_DIR` for overriding `~/.cache/toolbox/`? Likely yes for CI and testing.
- **Auth token source** — For private repos, where does the GitHub token come from? Likely `GITHUB_TOKEN` env var initially, with credential helper support later.
- **CLI binary name** — Should `toolbox resolve` be a subcommand of a unified `toolbox` binary, or a separate `toolbox-resolve` binary like `toolbox-pack`? The RFC implies a unified CLI.
- **Toolset file discovery** — Should `toolbox resolve` search up directories for `toolbox.toolset.json` (like `go.mod` discovery), or require explicit path?

For all design questions, consult `docs/rfc-tool-registry.md` first — most answers are there.
