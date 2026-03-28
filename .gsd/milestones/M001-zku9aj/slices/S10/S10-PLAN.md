# S10: Lockfile generation and verification

**Goal:** Implement declarative lockfile generation and verification so resolving any `*.toolset.json` writes a sibling `*.toolset.lock`, subsequent resolves verify cached packages against committed lock metadata, and declarative resolution still rides the existing Builder/Resolver path from S09.
**Demo:** After this: After this: TBD

## Tasks
- [x] **T01: Added schema-validated toolset lockfile helpers, deterministic sibling filename derivation, and contract tests for declarative toolset lockfiles.** — ## Description

Add toolset-level lockfile types and helpers beside `ToolsetFile`. This task establishes the file contract for S10: derive the sibling `*.toolset.lock` filename from any `*.toolset.json`, validate lockfile shape with an embedded JSON Schema, run Go semantic validation on the decoded entries, and support deterministic JSON encoding keyed by `<module>@<version>` without changing resolver behavior yet.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| `os.ReadFile` / `os.WriteFile` | Return the filesystem error with lockfile or toolset filename context and do not partially accept or rewrite lock state. | N/A for local file I/O; tests should cover ordinary read/write failures indirectly through missing files and malformed content. | N/A |
| Embedded JSON Schema resolution and validation | Fail fast with lockfile path context when the file shape is wrong and do not continue to semantic validation or resolution. | N/A | Reject wrong top-level structure, wrong field types, missing required fields, and unknown shape errors before any resolver work can trust the file. |
| Go semantic validation helpers | Reject invalid package keys or provenance semantics with entry-specific context after schema validation succeeds. | N/A | Never accept partially valid lock entries; fail before later resolve code can trust them. |

## Load Profile

- **Shared resources**: Repo-local toolset and lock files only.
- **Per-operation cost**: One sibling filename derivation plus one JSON read/write per lockfile load or persist.
- **10x breakpoint**: Deterministic encoding and error locality matter more than throughput; no meaningful performance bottleneck is expected here.

## Negative Tests

- **Malformed inputs**: invalid JSON, wrong top-level shape, missing required entry fields, wrong field types, invalid `<module>@<version>` package keys, and unsupported filenames.
- **Error paths**: malformed lockfile content fails before any resolver work is attempted, with schema-vs-semantic failures distinguishable in tests.
- **Boundary conditions**: `toolbox.toolset.json` and arbitrary names like `support-agent.toolset.json` both derive the correct sibling `.lock` path; write/read round-trips preserve deterministic package ordering.

## Steps

1. Add `toolset/toolset_lock.go` plus an embedded lockfile schema file, mirroring S09's `toolset` schema-loading pattern for shape validation.
2. Implement lockfile load, schema validate, semantic validate, and write helpers that keep JSON output stable and reject malformed entries with path and key context.
3. Add focused lockfile contract tests in `toolset/toolset_lock_test.go`, and store the source filename on `ToolsetFile` so later resolve work can reuse the derived lock path instead of guessing it.

## Must-Haves

- [ ] Toolset lockfiles are keyed by `<module>@<version>` and carry `archive_sha256`, `git_sha`, `resolved_from`, and `resolved_at`.
- [ ] Lockfile shape validation follows the S09 pattern: embedded JSON Schema first, then semantic Go validation.
- [ ] Any `*.toolset.json` filename derives a sibling `*.toolset.lock` path deterministically.
- [ ] Malformed lockfiles fail with specific file or entry context, and deterministic write/read behavior is covered by tests.
  - Estimate: 40m
  - Files: toolset/toolset_lock.go, toolset/toolset_lock_test.go, toolset/toolbox.toolset.lock.schema.json, toolset/toolset_file.go, docs/rfc-tool-registry.md
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetLock|TestToolsetFileLoad' -timeout 30s
- [x] **T02: Added provenance-aware resolver results, exact git-SHA capture, and one-refetch cache integrity enforcement without forking the builder path.** — ## Description

Widen the registry source and resolver seam so declarative resolve can capture the metadata required by R008 without introducing a second resolution path. This task should preserve S08/S09 fallback behavior while returning `archive_sha256`, exact `git_sha`, `resolved_from`, and `resolved_at`, and it should make cache hits refetch rather than silently trust bytes that disagree with the lock.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| GitHub release metadata and tag lookup HTTP calls | Return source-specific context; non-404 errors must still short-circuit before fallback. | Respect `context.Context` and return timeout/cancel errors without partially populating cache metadata. | Reject malformed release or tag/ref payloads before cache write or metadata persistence. |
| `git` clone / checkout / `rev-parse` in `GitSourceFallback` | Return step-specific git errors with module/version context and do not produce partially trusted metadata. | Respect caller cancellation and fail the fetch cleanly. | Reject timestamp or commit mismatches before packaging succeeds. |
| Cache load / mismatch handling in `registry.Resolver` | Treat mismatched cached bytes as untrusted, refetch once, and fail if the fetched bytes still disagree with the expected lock metadata. | Do not spin or retry indefinitely; one refetch attempt is the contract. | Surface expected-vs-actual hash context rather than collapsing everything into a generic load failure. |

## Load Profile

- **Shared resources**: Cache directory, ordered source chain, git subprocesses, and GitHub API/emulate responses.
- **Per-operation cost**: One resolver call per package; on GitHub release success that may include release metadata, tag/commit lookup, and two asset downloads; on fallback it may include one clone plus packaging.
- **10x breakpoint**: API round-trips and cache rewrites become the first pressure points; correctness of fallback and refetch behavior matters more than parallelism in this slice.

## Negative Tests

- **Malformed inputs**: malformed release/tag lookup payloads, bad cached archive bytes, and invalid expected lock metadata.
- **Error paths**: primary non-404 errors must still block fallback; cache mismatch must trigger exactly one refetch; a second mismatch must fail.
- **Boundary conditions**: cache hits return enough metadata for lock verification, fallback success records fallback provenance instead of primary provenance, and pseudo-version or tagged git fetches expose the exact commit SHA.

## Steps

1. Introduce resolver/source metadata types in `registry/source.go` and `registry/resolver.go`, and keep `Builder.AddFromRegistry` compatible by consuming the richer result internally rather than forking resolution behavior.
2. Capture provenance and commit identity in both source paths: GitHub release should resolve the tag to a commit SHA, while git fallback should return the checked-out commit SHA for tagged and pseudo-version fetches.
3. Extend `registry/resolver_test.go`, `registry/source_test.go`, and `registry/git_source_test.go` to prove cache-hit metadata, fallback provenance, exact `git_sha`, and refetch/fail semantics; use emulate or focused HTTP stubs where needed for tag lookup coverage.

## Must-Haves

- [ ] Resolver/source results expose the lock metadata required by R008 without bypassing the existing builder/resolver path.
- [ ] Cache hash mismatches refetch instead of being silently trusted, and persistent mismatches fail with explicit context.
- [ ] GitHub release and git-source fallback both record deterministic provenance and exact commit identity, with compatibility tests guarding `Builder.AddFromRegistry`.
  - Estimate: 55m
  - Files: registry/source.go, registry/source_test.go, registry/git_source.go, registry/git_source_test.go, registry/resolver.go, registry/resolver_test.go, toolset/toolset.go, registry/testutil/emulatetest/seed.go
  - Verify: GOWORK=$(pwd)/go.work go test ./registry -v -count=1 -run 'TestResolver|TestGitHubReleaseSource|TestGitSource' -timeout 60s
- [x] **T03: Declarative toolset resolves now verify and rewrite sibling lockfiles through the shared builder/resolver path.** — ## Description

Integrate the new lockfile contract into declarative resolution without bypassing the builder/resolver chain. `ToolsetFile.Resolve` should load an existing sibling lockfile, rely on the schema-plus-semantic validation introduced in T01, resolve packages in sorted order using the metadata-aware resolver seam, verify lock expectations before trusting cache state, and write the updated lockfile only after all packages succeed.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| Existing sibling lockfile load/schema-validation | Abort before resolution starts and leave the current lockfile untouched. | N/A for local file reads. | Reject malformed shape errors separately from semantic entry errors so callers can localize the problem quickly. |
| Metadata-aware resolver path from T02 | Stop on the first sorted package failure and propagate resolver or mismatch context unchanged. | Respect caller cancellation across multi-package resolve loops. | Never downgrade hash mismatches or missing provenance into a generic success. |
| Final lockfile write | Return the write error and preserve the previous on-disk lockfile contents. | N/A | Never leave a partially rewritten lockfile behind after a mid-write failure. |

## Load Profile

- **Shared resources**: Repo-local lockfile plus resolver cache and source chain.
- **Per-operation cost**: One lock lookup and one resolver call per declared package, followed by a single lockfile write after full success.
- **10x breakpoint**: Resolve remains linear in package count; deterministic ordering and single-write semantics matter more than concurrency here.

## Negative Tests

- **Malformed inputs**: malformed existing lockfile, schema-valid but semantically invalid lock entries, missing or stale lock entries, and mismatched cached archive hashes.
- **Error paths**: stale cache refetch succeeds when fresh bytes match the lock; refetched mismatch still fails; a package resolution failure leaves the prior lockfile untouched.
- **Boundary conditions**: first resolve with no lockfile writes one, later resolves accept matching cache hits, and sorted package ordering still determines the first failing package.

## Steps

1. Extend `toolset.Load` / `ToolsetFile` to retain the source filename and derived lock path so resolve-time code always knows which sibling lockfile it owns.
2. Update `ToolsetFile.Resolve` to use the metadata-aware resolver contract from T02, verify existing lock expectations before accepting cache results, and accumulate deterministic updated entries while keeping S09's sorted stop-on-first-error behavior.
3. Expand `toolset/toolset_file_test.go` to cover first-write, subsequent verification, schema-vs-semantic validation failures, cache-mismatch refetch, persistent mismatch failure, and no-partial-rewrite behavior, then run the focused toolset and registry regressions together.

## Must-Haves

- [ ] Successful declarative resolves write or update the sibling `*.toolset.lock` deterministically.
- [ ] Subsequent resolves validate existing lockfiles via schema plus semantic checks before trusting cache state.
- [ ] Subsequent resolves verify lock expectations before trusting cache state and fail cleanly when fresh content still disagrees with the lock.
- [ ] Declarative resolve still goes through `Builder.AddFromRegistry` / `registry.Resolver` and preserves sorted, stop-on-first-error behavior from S09.
  - Estimate: 50m
  - Files: toolset/toolset_file.go, toolset/toolset_file_test.go, toolset/toolset_lock.go, toolset/toolset_lock_test.go, toolset/toolset.go, registry/resolver.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetFileResolve|TestToolsetLock' -timeout 30s && GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s
