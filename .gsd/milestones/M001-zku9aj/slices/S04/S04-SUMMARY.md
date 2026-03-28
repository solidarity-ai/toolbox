---
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
