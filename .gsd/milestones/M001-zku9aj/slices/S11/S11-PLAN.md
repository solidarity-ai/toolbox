# S11: Replace directives

**Goal:** Implement `toolbox.toolset.local.json` replace overlays so declarative toolsets can redirect selected module paths to local package directories during resolve, while unreplaced packages stay on the existing registry/lock path and replaced packages do not rewrite lock entries.
**Demo:** After this: After this: TBD

## Tasks
- [x] **T01: Added validated *.toolset.local.json overlay loading and sibling filename helpers for declarative toolsets.** — Add the separate `toolbox.toolset.local.json` file contract that S11 depends on, without changing resolve behavior yet. This task should introduce a small overlay type plus sibling-filename derivation helper in `toolset`, validate the overlay with the same schema-first then semantic-validation pattern used for `toolbox.toolset.json` and `*.toolset.lock`, and prove the contract with focused tests so T02 can consume a boring, trusted loader instead of mixing file-shape work into resolver changes.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| Local overlay file reads via `os.ReadFile` | Return the filesystem error with the overlay filename and do not treat it as a valid replace map. Missing overlay should be handled as an explicit no-overlay case rather than a hard failure. | N/A for local file I/O. | N/A |
| Embedded local-overlay JSON Schema resolution and validation | Fail fast with overlay path context and do not continue into semantic validation or package resolution assumptions. | N/A | Reject wrong top-level structure, wrong field types, missing `replace` shape, and unknown fields before later code can trust the overlay. |
| Semantic module-path validation for `replace` keys | Reject invalid module-path keys with entry-specific context and never keep a partially accepted replace map. | N/A | Do not silently coerce or normalize malformed module identities. |

## Load Profile

- **Shared resources**: Repo-local toolset and overlay files only.
- **Per-operation cost**: One sibling filename derivation and one JSON read/validate pass per overlay load.
- **10x breakpoint**: Throughput is irrelevant here; deterministic validation and precise error locality matter more than speed.

## Negative Tests

- **Malformed inputs**: invalid JSON, wrong top-level shape, wrong `replace` value types, unknown fields, and invalid module-path keys.
- **Error paths**: malformed overlay content fails before any resolve logic can use it; a missing overlay is treated as absent rather than invalid.
- **Boundary conditions**: both `toolbox.toolset.json` and named files like `support-agent.toolset.json` derive the correct sibling `.toolset.local.json` path.

## Steps

1. Add a small local-overlay type and sibling-filename derivation helper in `toolset`, matching the existing schema-loading pattern used by toolset files and lockfiles.
2. Implement overlay load and semantic validation so `replace` keys are parsed as canonical module paths and missing overlay files can be distinguished from malformed ones.
3. Add focused tests covering filename derivation, missing-overlay tolerance, parse/schema failures, and invalid module-path keys.

## Must-Haves

- [ ] `toolbox.toolset.local.json` is modeled as a separate optional overlay with a top-level `replace` object.
- [ ] Overlay validation follows the established toolset pattern: embedded JSON Schema first, then Go semantic validation of module-path keys.
- [ ] Overlay filename derivation is deterministic for default and named `*.toolset.json` files.
- [ ] Missing overlay is treated as "no local replacements," while malformed overlays fail with overlay-path context.

## Verification

- `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetLocal' -timeout 30s`
- `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetFileLoad' -timeout 30s`
  - Estimate: 40m
  - Files: toolset/toolset_local.go, toolset/toolset_local_test.go, toolset/toolbox.toolset.local.schema.json, toolset/toolset_file.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolsetLocal|TestToolsetFileLoad' -timeout 30s
- [ ] **T02: Wire replace-aware resolve branching and lock-preservation tests** — Consume the overlay contract from T01 inside `ToolsetFile.Resolve()` so declarative toolsets can mix local replacements and registry-backed packages without forking the builder path or regressing S10 lock guarantees. This task is the slice’s real integration point: load the optional overlay before resolution, keep sorted module ordering deterministic, call `Builder.AddFromDir` for replaced modules, preserve any pre-existing lock entries for those modules exactly as written, and prove with tests that bad replace paths fail early without rewriting the sibling lockfile.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| Overlay loader from T01 | Abort before package resolution starts and leave the existing lockfile untouched when the overlay is malformed. | N/A for local file I/O. | Reject invalid replace maps before any registry or local package load begins. |
| `packaging.LoadDev` through `Builder.AddFromDir` | Return package-load or filesystem context for bad replace directories and stop before later sorted modules run. | N/A for local package loads. | Reject non-package directories or malformed dev package manifests without mutating the lockfile. |
| Existing resolver/lock path for unreplaced modules | Preserve S09/S10 stop-on-first-error semantics and bubble resolver or integrity errors unchanged for unreplaced packages. | Respect caller cancellation across mixed local and registry resolution. | Do not let replace support hide cache or registry mismatches for unreplaced packages. |

## Load Profile

- **Shared resources**: Sibling overlay file, sibling lockfile, resolver cache, and local package directories.
- **Per-operation cost**: One overlay lookup per resolve plus one local package load or one resolver call per declared package.
- **10x breakpoint**: Linear package iteration remains fine; the important pressure point is preserving deterministic order and zero lock drift when local replacements are mixed with registry packages.

## Negative Tests

- **Malformed inputs**: malformed overlay file, invalid replace path, replace path pointing at a non-package directory, and existing lockfiles that are already malformed.
- **Error paths**: bad replace paths fail before later sorted modules and leave the previous lockfile bytes untouched; unreplaced package failures still stop on the first sorted module.
- **Boundary conditions**: a replaced package with an existing lock entry preserves it byte-for-byte, a replaced package with no lock entry does not create one, and mixed replace + registry resolution still produces deterministic package order.

## Steps

1. Update `ToolsetFile.Resolve()` to load the optional sibling local overlay before module iteration and keep the existing lockfile validation flow unchanged.
2. During sorted package iteration, branch on `replace[module]`: call `Builder.AddFromDir` for replaced modules and preserve any existing lock entry unchanged; otherwise keep using `Builder.AddFromRegistryWithExpected` and accumulate updated lock metadata exactly as S10 does today.
3. Extend resolve tests to prove mixed replace + registry ordering, untouched lock entries for replaced modules, no new lock entry creation for replaced-only packages, and no partial lock rewrite on replace failure.

## Must-Haves

- [ ] Replaced modules resolve from local directories through the existing builder/dev-package path, while unreplaced modules still use the registry resolver path.
- [ ] Replace lookups honor deterministic sorted module iteration and do not reintroduce map-order nondeterminism.
- [ ] Existing lock entries for replaced modules remain unchanged and replaced-only modules never synthesize new lock entries.
- [ ] Any replace failure leaves the prior lockfile bytes untouched and surfaces file/path context that distinguishes overlay, directory, and package-load failures.

## Verification

- `GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolset(Local|FileResolve)' -timeout 30s`
- `GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s`
  - Estimate: 55m
  - Files: toolset/toolset_file.go, toolset/toolset_file_test.go, toolset/toolset_local.go, toolset/toolset.go, toolset/toolset_lock.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run 'TestToolset(Local|FileResolve)' -timeout 30s && GOWORK=$(pwd)/go.work go test ./toolset/... ./registry/... -v -count=1 -timeout 60s
