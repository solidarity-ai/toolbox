# S04: Local cache layout

**Goal:** Provide a local filesystem cache at `~/.cache/toolbox/pkg/` that stores and loads resolved packages keyed by module path + version, with sha256 verification via `packaging.LoadArchive`.
**Demo:** After this: # S04: Local cache layout — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T16:25:10.315Z

# S04: Local cache layout — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: Pure filesystem cache with no runtime behavior — validated entirely by unit tests.

## Preconditions

- Working directory: the M001-zku9aj worktree root
- Go toolchain available

## Smoke Test

Run `go test ./registry/... -v -count=1 -run TestCache` — all 6 subtests pass.

## Test Cases

### 1. Put + Has round-trip

1. Run `go test ./registry/... -v -count=1 -run TestCache/PutHasRoundTrip`
2. **Expected:** Passes. After Put, Has returns true for the same module+version.

### 2. Has returns false for missing entry

1. Run `go test ./registry/... -v -count=1 -run TestCache/HasMissingEntry`
2. **Expected:** Passes. Has returns false for a never-stored module+version.

### 3. Put + LoadArchive round-trip with real fixture

1. Run `go test ./registry/... -v -count=1 -run TestCache/PutLoadArchiveRoundTrip`
2. **Expected:** Passes. Archive written via Put loads successfully through packaging.LoadArchive with sha256 verification.

### 4. Path structure verification

1. Run `go test ./registry/... -v -count=1 -run TestCache/PathStructureVerification`
2. **Expected:** Passes. Files land at `<root>/<module>/@v/<version>.pkg`, `.manifest`, and `.info`.

### 5. TOOLBOX_CACHE_DIR override

1. Run `go test ./registry/... -v -count=1 -run TestCache/ToolboxCacheDirOverride`
2. **Expected:** Passes. Setting TOOLBOX_CACHE_DIR changes the root directory used by NewCache("").

### 6. LoadArchive on missing entry returns error

1. Run `go test ./registry/... -v -count=1 -run TestCache/LoadArchiveMissingEntryReturnsError`
2. **Expected:** Passes. LoadArchive returns a non-nil error for a missing module+version.

## Edge Cases

### Empty cache directory

1. Create a new Cache with a fresh temp dir, call Has — returns false, call LoadArchive — returns error.
2. **Expected:** No panics, clean error messages.

## Failure Signals

- Any subtest in `go test ./registry/... -run TestCache` failing
- `go vet ./registry/...` reporting issues

## Not Proven By This UAT

- Cache eviction or size management (not implemented)
- Concurrent write safety beyond OS-level guarantees
- Integration with downstream resolvers (S05, S06)

## Notes for Tester

Tests run in ~6ms total. No external dependencies required.


## Tasks
- [x] **T01: Added the registry filesystem cache with env-aware root selection, deterministic package paths, and real archive round-trip tests.** — Implement `registry/cache.go` with a `Cache` struct holding a root directory. Constructor `NewCache(dir string)` defaults to `os.UserCacheDir()/toolbox/pkg` when dir is empty, and respects `TOOLBOX_CACHE_DIR` env var. Methods: `Has(module ModulePath, version Version) bool`, `Put(module ModulePath, version Version, archiveBytes, manifestBytes []byte) error`, `LoadArchive(module ModulePath, version Version) (packaging.LoadedPackage, error)`. Internal path helper computes `<root>/<module>/@v/<version>.{pkg,manifest,info}`. Put writes all three files (info can be a JSON blob with version string). LoadArchive delegates to `packaging.LoadArchive(archivePath, manifestPath)`. Then implement `registry/cache_test.go` with table-driven tests covering: (1) Put+Has round-trip, (2) Has returns false for missing entry, (3) Put+LoadArchive round-trip with a real archive from packaging test fixtures, (4) path structure verification (check files land at expected paths), (5) TOOLBOX_CACHE_DIR override, (6) LoadArchive on missing entry returns error.
  - Estimate: 45m
  - Files: registry/cache.go, registry/cache_test.go
  - Verify: go test ./registry/... -v -count=1 -run TestCache && go vet ./registry/...
