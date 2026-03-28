# S09: Toolset file parsing

**Goal:** Implement declarative `toolbox.toolset.json` loading so a caller can parse a toolset file, validate its package/tool references up front, and resolve declared packages through `Builder.AddFromRegistry` into the same `ResolvedToolset` shape produced by imperative builder usage.
**Demo:** After this: After this: TBD

## Tasks
- [x] **T01: Added validated `toolbox.toolset.json` loading and parse-time contract tests.** — ## Description
Add `toolset/toolset_file.go` with exported `ToolsetFile` and `ToolEntry` types plus `Load(filename)` for the minimal S09 file format. The loader must read `toolbox.toolset.json`, unmarshal JSON, and validate the declared `packages` and `tools` before any registry resolution occurs so malformed configuration fails fast.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| `os.ReadFile` | Return the read error with filename context and do not partially populate the toolset. | N/A for local file reads; tests should assert ordinary read failures such as missing files instead. | N/A |
| `tool.ParseModulePath` / `tool.ParseVersion` / `tool.ParseToolFQN` | Bubble field-specific validation errors so callers can localize the bad entry. | N/A | Reject the offending package or tool entry before any resolver work begins. |

## Load Profile

- **Shared resources**: Local filesystem reads only; no shared network or cache state.
- **Per-operation cost**: One JSON file read plus linear validation over package and tool entries.
- **10x breakpoint**: Error-message clarity and deterministic validation matter before raw performance; no scaling bottleneck is expected in this slice.

## Negative Tests

- **Malformed inputs**: missing file, invalid JSON, empty `packages`, invalid module path, invalid version, invalid tool FQN.
- **Error paths**: tool references an undeclared package; tool version mismatches the declared package version.
- **Boundary conditions**: a valid file with multiple entries preserves declared tools and keeps validation deterministic.

## Steps

1. Add exported types and JSON decoding in `toolset/toolset_file.go`, reusing the existing `tool.Parse*` helpers instead of duplicating parsing logic.
2. Validate `packages` and `tools`, enforcing non-empty collections, exact-version package entries, full tool FQNs, declared-package membership, and version equality between each tool FQN and its package entry.
3. Add focused tests in `toolset/toolset_file_test.go` covering the happy path and all parse-time failure modes required by R007.

## Must-Haves

- [ ] `Load(filename)` returns a validated `*ToolsetFile` with exported `Packages` and `Tools` fields.
- [ ] Parse-time validation rejects malformed config before any resolver call is possible.
- [ ] `toolset/toolset_file_test.go` names the parse contract and failure cases explicitly.
  - Estimate: 35m
  - Files: toolset/toolset_file.go, toolset/toolset_file_test.go, tool/fqn.go, toolset/toolset.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileLoad -timeout 30s
- [x] **T02: Added ToolsetFile.Resolve and cache-backed tests that drive declarative package resolution through the builder registry path.** — ## Description
Extend `toolset/toolset_file.go` with `(*ToolsetFile).Resolve(ctx, resolver)` so declarative toolset files resolve declared packages via the same builder/resolver/cache path introduced in S08. The method should use `NewWithResolver`, iterate packages in a deterministic order, call `AddFromRegistry` for each package, and return the resulting `ResolvedToolset` without introducing any direct resolver bypass.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| `toolset.NewWithResolver` / `Builder.AddFromRegistry` | Return `ErrNoResolver` or the wrapped builder/parse error immediately and stop resolving additional packages. | Respect the caller's `context.Context`; tests should keep resolution cache-backed so no long-running dependency exists. | Propagate resolver/cache load errors unchanged so callers can diagnose the failing package. |
| `registry.Resolver` cache path | Stop on the first package that cannot be resolved or loaded from cache; do not silently skip packages. | Use the passed context for future source-backed resolvers even though S09 tests run against a pre-populated cache. | Surface integrity/manifest errors from the resolver path instead of masking them. |

## Load Profile

- **Shared resources**: Resolver cache directory and ordered source chain accessed through `Builder.AddFromRegistry`.
- **Per-operation cost**: One resolver call per declared package plus package load into the builder.
- **10x breakpoint**: Resolution scales linearly with package count; deterministic iteration and cache-backed tests keep the contract stable without adding concurrency in this slice.

## Negative Tests

- **Malformed inputs**: nil resolver, package entries that were parse-valid but cannot be resolved, and empty/invalid parsed state rejected before or during resolution.
- **Error paths**: resolver/cache failure for a declared package stops resolution and surfaces the underlying error.
- **Boundary conditions**: pre-populated cache round-trip produces the same resolved package/tools as imperative builder usage.

## Steps

1. Implement `(*ToolsetFile).Resolve(ctx, resolver)` in `toolset/toolset_file.go` by creating a builder with `NewWithResolver`, sorting package module keys, and calling `AddFromRegistry` for each declared package.
2. Preserve the already-parsed `Tools` list on the `ToolsetFile` while returning the `ResolvedToolset` produced by the builder so downstream slices can consume the same parsed structure.
3. Extend `toolset/toolset_file_test.go` with resolve-focused tests that seed a temp cache with the existing dist fixture, prove nil-resolver behavior, and verify cache-backed declarative resolution succeeds without regressing S08 behavior.

## Must-Haves

- [ ] `ToolsetFile.Resolve` uses `Builder.AddFromRegistry` for every declared package instead of talking to the resolver directly.
- [ ] Package iteration is deterministic and tested.
- [ ] Resolve-focused tests prove nil-resolver handling and cache-backed success using the existing dist fixture pattern.
  - Estimate: 35m
  - Files: toolset/toolset_file.go, toolset/toolset_file_test.go, toolset/toolset.go, registry/resolver.go, registry/cache.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestToolsetFileResolve -timeout 30s
