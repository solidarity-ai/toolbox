---
id: T01
parent: S02
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/testutil/emulatetest/seed.go", "registry/testutil/emulatetest/emulatetest_test.go", ".gsd/milestones/M001-zku9aj/slices/S02/tasks/T01-SUMMARY.md"]
key_decisions: ["Reused a shared internal pack-and-upload pipeline so all failure helpers preserve the SeedPackageRelease contract while varying only asset bytes or presence.", "Validated corrupt archive behavior through the exported packaging.LoadArchive API instead of importing packaging/internal/archive from a sibling package test."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran the task-plan verification command `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` and it passed, covering the existing lifecycle and normal seeding tests plus the new corrupt archive, mismatched hash, missing asset, and empty release scenarios. An LSP diagnostics check was attempted afterward, but no Go language server was available in this environment."
completed_at: 2026-03-27T01:27:06.153Z
blocker_discovered: false
---

# T01: Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.

> Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.

## What Happened
---
id: T01
parent: S02
milestone: M001-zku9aj
key_files:
  - registry/testutil/emulatetest/seed.go
  - registry/testutil/emulatetest/emulatetest_test.go
  - .gsd/milestones/M001-zku9aj/slices/S02/tasks/T01-SUMMARY.md
key_decisions:
  - Reused a shared internal pack-and-upload pipeline so all failure helpers preserve the SeedPackageRelease contract while varying only asset bytes or presence.
  - Validated corrupt archive behavior through the exported packaging.LoadArchive API instead of importing packaging/internal/archive from a sibling package test.
duration: ""
verification_result: passed
completed_at: 2026-03-27T01:27:06.179Z
blocker_discovered: false
---

# T01: Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.

**Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.**

## What Happened

Extended registry/testutil/emulatetest/seed.go with four exported helpers: SeedCorruptArchiveRelease, SeedMismatchedHashRelease, SeedMissingAssetRelease, and SeedEmptyRelease. Kept them aligned with the existing SeedPackageRelease contract by factoring common package packing and release-asset upload logic into shared helpers, then varying only the uploaded bytes or asset selection per scenario. Added package tests in registry/testutil/emulatetest/emulatetest_test.go to verify corrupt archives fail package loading, mismatched manifests disagree with the actual archive checksum, missing-asset releases expose exactly one expected asset, and empty releases expose zero assets. During verification, corrected the tests to use the exported packaging.LoadArchive wrapper instead of an internal package import, and relaxed the corrupt-archive assertion to require LoadArchive failure without assuming it fails specifically during extraction because checksum validation trips first for random bytes.

## Verification

Ran the task-plan verification command `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` and it passed, covering the existing lifecycle and normal seeding tests plus the new corrupt archive, mismatched hash, missing asset, and empty release scenarios. An LSP diagnostics check was attempted afterward, but no Go language server was available in this environment.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` | 0 | ✅ pass | 3693ms |


## Deviations

None.

## Known Issues

None.

## Files Created/Modified

- `registry/testutil/emulatetest/seed.go`
- `registry/testutil/emulatetest/emulatetest_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S02/tasks/T01-SUMMARY.md`


## Deviations
None.

## Known Issues
None.
