# Requirements

This file is the explicit capability and coverage contract for the project.

## Validated

### R001 — Every tool is uniquely identified by `{module_path}@{version}/{tool_path}`. Module paths, versions, tool paths, and full FQNs are parseable, formattable, and round-trip faithful. Pseudo-versions (v0.0.0-timestamp-commitsha) are valid version strings.
- Class: core-capability
- Status: validated
- Description: Every tool is uniquely identified by `{module_path}@{version}/{tool_path}`. Module paths, versions, tool paths, and full FQNs are parseable, formattable, and round-trip faithful. Pseudo-versions (v0.0.0-timestamp-commitsha) are valid version strings.
- Why it matters: FQNs are the identity foundation — every other capability (cache, resolve, toolset) keys off them
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S03
- Supporting slices: none
- Validation: Table-driven tests prove parse/format round-trips for ModulePath, Version, ToolPath, ToolFQN, PackageVer. Pseudo-version decomposition verified. All edge cases covered.
- Notes: See `docs/rfc-tool-registry.md` §1 for full FQN spec

### R002 — Resolved packages are stored in a content-addressable local cache at `~/.cache/toolbox/pkg/` keyed by module path + version. Archives are verified on load via sha256. The cache layout mirrors the proxy protocol path structure.
- Class: core-capability
- Status: validated
- Description: Resolved packages are stored in a content-addressable local cache at `~/.cache/toolbox/pkg/` keyed by module path + version. Archives are verified on load via sha256. The cache layout mirrors the proxy protocol path structure.
- Why it matters: Avoids re-downloading packages on every resolve; enables offline work after first fetch
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S04
- Supporting slices: none
- Validation: TestCache suite proves: packages stored at ~/.cache/toolbox/pkg/<module>/@v/<version>.{pkg,manifest,info}, round-trip through LoadArchive with sha256 verification, env override works, missing entries detected.
- Notes: See `docs/rfc-tool-registry.md` §3 for cache layout spec

### R003 — Given a module path + version, the resolver fetches `.toolbox.pkg` and `toolbox.pkg.json` from the corresponding GitHub Release's assets. Supports `GITHUB_BASE_URL` override for testing against emulate. Handles auth via token for private repos.
- Class: core-capability
- Status: validated
- Description: Given a module path + version, the resolver fetches `.toolbox.pkg` and `toolbox.pkg.json` from the corresponding GitHub Release's assets. Supports `GITHUB_BASE_URL` override for testing against emulate. Handles auth via token for private repos.
- Why it matters: GitHub Releases is the primary distribution path — most packages will be fetched this way
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S05
- Supporting slices: none
- Validation: Combined milestone evidence validates R003: S05 proved GitHub release fetch semantics and GITHUB_BASE_URL override through emulate-backed integration tests, S12 proved GITHUB_TOKEN Authorization header wiring at the CLI boundary without token leakage, and final closeout verification `GOWORK=$(pwd)/go.work go test ./... -count=1` passed with registry and CLI packages green.
- Notes: See `docs/rfc-tool-registry.md` §2 for GitHub Releases source spec

### R004 — When no release assets exist, the resolver clones the git repo at the tagged version, reads the package source, runs `packaging.Pack` locally, and caches the result. Uses the same `PackageSource` interface as GitHub Releases.
- Class: core-capability
- Status: validated
- Description: When no release assets exist, the resolver clones the git repo at the tagged version, reads the package source, runs `packaging.Pack` locally, and caches the result. Uses the same `PackageSource` interface as GitHub Releases.
- Why it matters: Ensures the system works with any git host, even without CI-published release artifacts
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S06
- Supporting slices: none
- Validation: Validated by S06 focused git-source proof: `go test ./registry -v -count=1 -run TestGitSource -timeout 30s` passed, proving tagged git fallback clone + local packaging, archive/manifest return values, and correct failure behavior for missing tags, broken repos, and corrupt package manifests.
- Notes: See `docs/rfc-tool-registry.md` §2 (git-source fallback) and §3

### R005 — Packages can be pinned to a specific untagged git commit using pseudo-version format `v0.0.0-{yyyyMMddHHmmss}-{12-char commit SHA prefix}`. The resolver fetches the specified commit, runs Pack locally, and caches the result.
- Class: core-capability
- Status: validated
- Description: Packages can be pinned to a specific untagged git commit using pseudo-version format `v0.0.0-{yyyyMMddHHmmss}-{12-char commit SHA prefix}`. The resolver fetches the specified commit, runs Pack locally, and caches the result.
- Why it matters: Enables depending on unreleased commits during development and testing
- Source: user
- Primary owning slice: M001-zku9aj/S07
- Supporting slices: none
- Validation: Validated by focused pseudo-version proof at both source and resolver/cache layers: `GOWORK=$(pwd)/go.work go test ./registry -v -count=1 -run 'TestResolver/PseudoVersionFetchPopulatesCacheAndSecondResolveHitsCache|TestGitSource/pseudo_version_happy_path' -timeout 60s` passed, proving fetch of the targeted untagged commit, local packaging, cache population, and cache reuse on the second resolve.
- Notes: Validation no longer relies on composition alone; resolver-level pseudo-version cache proof now exists directly in `registry/resolver_test.go`.

### R006 — `toolset.Builder` gains `AddFromRegistry(modulePath, version string)` that resolves, downloads/caches, and loads a package — producing the same `LoadedPackage` that `AddFromDir` and `AddFromArchive` produce.
- Class: core-capability
- Status: validated
- Description: `toolset.Builder` gains `AddFromRegistry(modulePath, version string)` that resolves, downloads/caches, and loads a package — producing the same `LoadedPackage` that `AddFromDir` and `AddFromArchive` produce.
- Why it matters: This is the integration point — downstream code (invoke, codemode) doesn't change; the Builder just has a new way to acquire packages
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S08
- Supporting slices: none
- Validation: TestBuilderAddFromRegistry proves: nil-resolver returns ErrNoResolver, invalid inputs return parse errors, pre-populated cache resolves successfully producing correct package name, resolved package appears in Resolve() output identically to AddFromDir/AddFromArchive.
- Notes: See `docs/rfc-tool-registry.md` §3 (AddFromRegistry)

### R007 — A `toolbox.toolset.json` file declares package dependencies (module path → version) and a tools list. `toolset.Load()` parses the file and resolves all packages via the registry.
- Class: core-capability
- Status: validated
- Description: A `toolbox.toolset.json` file declares package dependencies (module path → version) and a tools list. `toolset.Load()` parses the file and resolves all packages via the registry.
- Why it matters: Declarative toolset assembly replaces imperative Builder calls for config-driven use cases
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S09
- Supporting slices: none
- Validation: Validated by S09 declarative toolset proof: `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileLoad -timeout 30s`, `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileResolve -timeout 30s`, and `GOWORK=$(pwd)/go.work go test ./toolset/... -v -count=1 -timeout 30s` passed, proving schema + semantic validation and registry-backed declarative resolution through the shared builder path.
- Notes: See `docs/rfc-tool-registry.md` §4. First version: packages + tools only. Bindings, credentials, context deferred.

### R008 — `toolbox resolve` writes a `toolbox.toolset.lock` recording archive_sha256, git_sha, resolved_from, and resolved_at for each package. On subsequent resolves, the lockfile is verified — mismatched hashes error.
- Class: core-capability
- Status: validated
- Description: `toolbox resolve` writes a `toolbox.toolset.lock` recording archive_sha256, git_sha, resolved_from, and resolved_at for each package. On subsequent resolves, the lockfile is verified — mismatched hashes error.
- Why it matters: Reproducibility and integrity — same lockfile = same packages on any machine
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S10
- Supporting slices: none
- Validation: `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetFileResolve|TestToolsetLock' -timeout 30s && GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s` passed, proving declarative resolves write sibling `*.toolset.lock` files with archive_sha256/git_sha/resolved_from/resolved_at metadata and subsequently verify cache state against committed lock expectations.
- Notes: See `docs/rfc-tool-registry.md` §4 (lockfile)

### R009 — A `toolbox.toolset.local.json` overlay file (gitignored) can redirect any module path to a local directory. The resolver loads from the local dir (dev mode) instead of fetching remotely. The lockfile entry for replaced packages is not updated.
- Class: core-capability
- Status: validated
- Description: A `toolbox.toolset.local.json` overlay file (gitignored) can redirect any module path to a local directory. The resolver loads from the local dir (dev mode) instead of fetching remotely. The lockfile entry for replaced packages is not updated.
- Why it matters: Essential for the "developing a package and consuming it" workflow
- Source: user (RFC)
- Primary owning slice: M001-zku9aj/S11
- Supporting slices: none
- Validation: Validated by S11/S12 overlay proof: `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolset(Local|FileResolve)' -timeout 30s`, `GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s`, and the S12 focused CLI suite passed, proving sibling overlay loading, local-dir replacement, preserved lock entries, no synthetic lock entries for replaced-only packages, and CLI-level overlay behavior.
- Notes: See `docs/rfc-tool-registry.md` §7 (replace directives)

### R010 — `toolbox resolve` reads a toolset file and resolves all packages. `toolbox versions` lists available versions. `toolbox resolve --upgrade` bumps packages. All commands support `--file` for toolset file selection.
- Class: core-capability
- Status: validated
- Description: `toolbox resolve` reads a toolset file and resolves all packages. `toolbox versions` lists available versions. `toolbox resolve --upgrade` bumps packages. All commands support `--file` for toolset file selection.
- Why it matters: Makes the workflow tangible — humans and CI can invoke resolution directly
- Source: user
- Primary owning slice: M001-zku9aj/S12
- Supporting slices: none
- Validation: Validated by S12 focused CLI proof: `GOWORK=$(pwd)/go.work go test ./cmd/toolbox/... -v -count=1 -run 'TestRun(Versions|Resolve)' -timeout 120s` passed, covering `toolbox versions`, `toolbox resolve`, single-module `toolbox resolve --upgrade`, `--file` selection, committed-lock cache-hit re-resolve, sibling local-overlay behavior, token-auth header wiring without token leakage, and CLI validation failures.
- Notes: S12 closes R010 on the real `cmd/toolbox` boundary rather than on library seams alone.

### R011 — Shared Go test fixture starts vercel-labs/emulate once per test binary, seeds repos with real .toolbox.pkg release assets and local git repos at tags. Builder API supports constructing failure scenarios (corrupt archives, mismatched hashes, missing assets, broken git repos). Works in GitHub Actions CI. GitHub-related verification must be satisfiable without pushing to `main`; acceptable proof comes from local runs, CI/workflow validation, emulate-backed integration tests, and read-only GitHub inspection.
- Class: quality-attribute
- Status: validated
- Description: Shared Go test fixture starts vercel-labs/emulate once per test binary, seeds repos with real .toolbox.pkg release assets and local git repos at tags. Builder API supports constructing failure scenarios (corrupt archives, mismatched hashes, missing assets, broken git repos). Works in GitHub Actions CI. GitHub-related verification must be satisfiable without pushing to `main`; acceptable proof comes from local runs, CI/workflow validation, emulate-backed integration tests, and read-only GitHub inspection.
- Why it matters: Every resolver test exercises real HTTP against a real GitHub API emulator — no mocked HTTP clients — while keeping GitHub testing off the protected mainline
- Source: user
- Primary owning slice: M001-zku9aj/S01
- Supporting slices: M001-zku9aj/S02
- Validation: Validated by the full milestone artifact chain: S01-S02 delivered singleton emulate lifecycle, real release-asset seed builders, git fixture builders, failure-scenario builders, and CI workflow support; the requirement explicitly accepts proof from local runs, CI/workflow validation, emulate-backed integration tests, and read-only GitHub inspection without pushing to main; final closeout verification `GOWORK=$(pwd)/go.work go test ./... -count=1` passed including registry/testutil/emulatetest and registry/testutil/gitfixture.
- Notes: Uses https://github.com/vercel-labs/emulate. Remote GitHub testing, if ever needed, must use a non-`main` branch and explicit user confirmation.

## Deferred

### R020 — Registry proxy at proxy.include.tools providing discovery, stats, availability caching, and download pass-through
- Class: core-capability
- Status: deferred
- Description: Registry proxy at proxy.include.tools providing discovery, stats, availability caching, and download pass-through
- Why it matters: Ecosystem discovery and organizational visibility
- Source: user (RFC)
- Primary owning slice: none
- Supporting slices: none
- Validation: unmapped
- Notes: Deferred to separate milestone. See `docs/rfc-tool-registry.md` §2 (registry proxy) and §9

### R021 — Proxy search API and `toolbox search` CLI command for ecosystem-wide package discovery
- Class: core-capability
- Status: deferred
- Description: Proxy search API and `toolbox search` CLI command for ecosystem-wide package discovery
- Why it matters: Enables finding packages without knowing module paths
- Source: user (RFC)
- Primary owning slice: none
- Supporting slices: none
- Validation: unmapped
- Notes: Deferred to separate milestone. See `docs/rfc-tool-registry.md` §10

### R022 — Toolset file supports explicit aliases when two packages share the same short name
- Class: core-capability
- Status: deferred
- Description: Toolset file supports explicit aliases when two packages share the same short name
- Why it matters: Prevents ambiguity in short-name resolution
- Source: user (RFC)
- Primary owning slice: none
- Supporting slices: none
- Validation: unmapped
- Notes: Deferred — rare edge case for initial release. See `docs/rfc-tool-registry.md` §4 (aliasing)

### R023 — Full toolset file format with resource_bindings, credentials, context sections
- Class: core-capability
- Status: deferred
- Description: Full toolset file format with resource_bindings, credentials, context sections
- Why it matters: Completes the declarative toolset format for production use
- Source: user (RFC)
- Primary owning slice: none
- Supporting slices: none
- Validation: unmapped
- Notes: Deferred — orthogonal to registry resolution. See `docs/rfc-tool-registry.md` §4 and §8

### R024 — solidarity-ai/toolbox-pack-action that automates Pack + GitHub Release on tag push
- Class: operability
- Status: deferred
- Description: solidarity-ai/toolbox-pack-action that automates Pack + GitHub Release on tag push
- Why it matters: One-step publish workflow for package authors
- Source: user (RFC)
- Primary owning slice: none
- Supporting slices: none
- Validation: unmapped
- Notes: Deferred — separate repo/effort. See `docs/rfc-tool-registry.md` §2 (GitHub Action)

## Out of Scope

### R030 — Detached signatures alongside archives, configurable trust store
- Class: compliance/security
- Status: out-of-scope
- Description: Detached signatures alongside archives, configurable trust store
- Why it matters: Prevents scope creep into cryptographic verification infrastructure
- Source: RFC (future)
- Primary owning slice: none
- Supporting slices: none
- Validation: n/a
- Notes: See `docs/rfc-tool-registry.md` §6 (package signing future)

### R031 — Packages declare network, exec, and other capabilities for transparency and future enforcement
- Class: compliance/security
- Status: out-of-scope
- Description: Packages declare network, exec, and other capabilities for transparency and future enforcement
- Why it matters: Prevents scope creep into capability policy infrastructure
- Source: RFC (future)
- Primary owning slice: none
- Supporting slices: none
- Validation: n/a
- Notes: See `docs/rfc-tool-registry.md` §6 (capability declarations future)

### R032 — Toolsets extending other toolsets via `extends` directive
- Class: core-capability
- Status: out-of-scope
- Description: Toolsets extending other toolsets via `extends` directive
- Why it matters: Prevents scope creep into merge semantics and conflict resolution
- Source: RFC (future)
- Primary owning slice: none
- Supporting slices: none
- Validation: n/a
- Notes: See `docs/rfc-tool-registry.md` alternatives considered

### R033 — Single git repo containing multiple packages with subdirectory module paths and prefixed version tags
- Class: core-capability
- Status: out-of-scope
- Description: Single git repo containing multiple packages with subdirectory module paths and prefixed version tags
- Why it matters: Adds complexity to git-based resolution — not needed for initial release
- Source: RFC (future)
- Primary owning slice: none
- Supporting slices: none
- Validation: n/a
- Notes: See `docs/rfc-tool-registry.md` open question §5

## Traceability

| ID | Class | Status | Primary owner | Supporting | Proof |
|---|---|---|---|---|---|
| R001 | core-capability | validated | M001-zku9aj/S03 | none | Table-driven tests prove parse/format round-trips for ModulePath, Version, ToolPath, ToolFQN, PackageVer. Pseudo-version decomposition verified. All edge cases covered. |
| R002 | core-capability | validated | M001-zku9aj/S04 | none | TestCache suite proves: packages stored at ~/.cache/toolbox/pkg/<module>/@v/<version>.{pkg,manifest,info}, round-trip through LoadArchive with sha256 verification, env override works, missing entries detected. |
| R003 | core-capability | validated | M001-zku9aj/S05 | none | Combined milestone evidence validates R003: S05 proved GitHub release fetch semantics and GITHUB_BASE_URL override through emulate-backed integration tests, S12 proved GITHUB_TOKEN Authorization header wiring at the CLI boundary without token leakage, and final closeout verification `GOWORK=$(pwd)/go.work go test ./... -count=1` passed with registry and CLI packages green. |
| R004 | core-capability | validated | M001-zku9aj/S06 | none | Validated by S06 focused git-source proof: `go test ./registry -v -count=1 -run TestGitSource -timeout 30s` passed, proving tagged git fallback clone + local packaging, archive/manifest return values, and correct failure behavior for missing tags, broken repos, and corrupt package manifests. |
| R005 | core-capability | validated | M001-zku9aj/S07 | none | Validated by focused pseudo-version proof at both source and resolver/cache layers: `GOWORK=$(pwd)/go.work go test ./registry -v -count=1 -run 'TestResolver/PseudoVersionFetchPopulatesCacheAndSecondResolveHitsCache|TestGitSource/pseudo_version_happy_path' -timeout 60s` passed, proving fetch of the targeted untagged commit, local packaging, cache population, and cache reuse on the second resolve. |
| R006 | core-capability | validated | M001-zku9aj/S08 | none | TestBuilderAddFromRegistry proves: nil-resolver returns ErrNoResolver, invalid inputs return parse errors, pre-populated cache resolves successfully producing correct package name, resolved package appears in Resolve() output identically to AddFromDir/AddFromArchive. |
| R007 | core-capability | validated | M001-zku9aj/S09 | none | Validated by S09 declarative toolset proof: `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileLoad -timeout 30s`, `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileResolve -timeout 30s`, and `GOWORK=$(pwd)/go.work go test ./toolset/... -v -count=1 -timeout 30s` passed, proving schema + semantic validation and registry-backed declarative resolution through the shared builder path. |
| R008 | core-capability | validated | M001-zku9aj/S10 | none | `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetFileResolve|TestToolsetLock' -timeout 30s && GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s` passed, proving declarative resolves write sibling `*.toolset.lock` files with archive_sha256/git_sha/resolved_from/resolved_at metadata and subsequently verify cache state against committed lock expectations. |
| R009 | core-capability | validated | M001-zku9aj/S11 | none | Validated by S11/S12 overlay proof: `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolset(Local|FileResolve)' -timeout 30s`, `GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s`, and the S12 focused CLI suite passed, proving sibling overlay loading, local-dir replacement, preserved lock entries, no synthetic lock entries for replaced-only packages, and CLI-level overlay behavior. |
| R010 | core-capability | validated | M001-zku9aj/S12 | none | Validated by S12 focused CLI proof: `GOWORK=$(pwd)/go.work go test ./cmd/toolbox/... -v -count=1 -run 'TestRun(Versions|Resolve)' -timeout 120s` passed, covering `toolbox versions`, `toolbox resolve`, single-module `toolbox resolve --upgrade`, `--file` selection, committed-lock cache-hit re-resolve, sibling local-overlay behavior, token-auth header wiring without token leakage, and CLI validation failures. |
| R011 | quality-attribute | validated | M001-zku9aj/S01 | M001-zku9aj/S02 | Validated by the full milestone artifact chain: S01-S02 delivered singleton emulate lifecycle, real release-asset seed builders, git fixture builders, failure-scenario builders, and CI workflow support; the requirement explicitly accepts proof from local runs, CI/workflow validation, emulate-backed integration tests, and read-only GitHub inspection without pushing to main; final closeout verification `GOWORK=$(pwd)/go.work go test ./... -count=1` passed including registry/testutil/emulatetest and registry/testutil/gitfixture. |
| R020 | core-capability | deferred | none | none | unmapped |
| R021 | core-capability | deferred | none | none | unmapped |
| R022 | core-capability | deferred | none | none | unmapped |
| R023 | core-capability | deferred | none | none | unmapped |
| R024 | operability | deferred | none | none | unmapped |
| R030 | compliance/security | out-of-scope | none | none | n/a |
| R031 | compliance/security | out-of-scope | none | none | n/a |
| R032 | core-capability | out-of-scope | none | none | n/a |
| R033 | core-capability | out-of-scope | none | none | n/a |

## Coverage Summary

- Active requirements: 0
- Mapped to slices: 0
- Validated: 11 (R001, R002, R003, R004, R005, R006, R007, R008, R009, R010, R011)
- Unmapped active requirements: 0
