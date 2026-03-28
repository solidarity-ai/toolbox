# S04: 

**Goal:** ---
id: S04
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - Cache type with Has/Put/LoadArchive for downstream resolvers to store and retrieve packages
requires:
  - slice: S03
    provides: ModulePath and Version types used as cache keys
affects:
  - S05
  - S06
key_files:
  - registry/cache.go
  - registry/cache_test.go
key_decisions:
  - Used registry-local aliases for ModulePath and Version backed by tool package types so the cache API stays concise while matching the existing canonical types.
  - Delegated cache archive validation to packaging.LoadArchive rather than re-implementing sha256 or manifest verification inside registry.
patterns_established:
  - Cache path layout: <root>/<module>/@v/<version>.{pkg,manifest,info} — mirrors Go module proxy protocol structure
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S04/tasks/T01-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:35:58.798Z
blocker_discovered: false
---

# S04: Local cache layout

**Filesystem cache at ~/.cache/toolbox/pkg/ stores and loads resolved packages keyed by module path + version with sha256 verification via packaging.LoadArchive.**

## What Happened

Implemented registry/cache.go with a Cache type that resolves its root directory from an explicit parameter, the TOOLBOX_CACHE_DIR env var, or os.UserCacheDir fallback. The cache stores packages under a deterministic path layout: `<root>/<module>/@v/<version>.{pkg,manifest,info}`. Put writes all three sidecar files atomically. Has checks for the archive file's existence. LoadArchive delegates to packaging.LoadArchive for sha256 verification against the manifest. Registry-local type aliases keep the cache API concise while staying compatible with the canonical tool package types. A comprehensive table-driven test suite in registry/cache_test.go covers Put+Has round-trips, missing entry detection, real archive round-tripping through packaging.LoadArchive using test fixtures, path structure verification, TOOLBOX_CACHE_DIR override, and missing-entry load error handling.

## Verification

All 6 TestCache subtests pass and `go vet ./registry/...` is clean.

## Requirements Advanced

- R002 — Implemented the cache layout at ~/.cache/toolbox/pkg/ with module+version keying and sha256 verification via packaging.LoadArchive

## Requirements Validated

- R002 — TestCache suite proves: packages stored at correct paths, round-trip through LoadArchive with sha256 verification, env override works, missing entries detected

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

None.

## Known Limitations

No cache eviction or size management — deferred to later work. No concurrent write protection beyond OS-level atomicity.

## Follow-ups

None.

## Files Created/Modified

- `registry/cache.go` — Cache type with Has/Put/LoadArchive methods and env-aware root selection
- `registry/cache_test.go` — Table-driven test suite covering 6 cache scenarios including real archive round-trips

**Demo:** After this: # S04: Local cache layout — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-28T00:35:58.798Z

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
- [x] **T01: Added the registry filesystem cache with env-aware root selection, deterministic package paths, and real archive round-trip tests.** — 
