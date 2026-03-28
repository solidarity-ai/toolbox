---
id: S02
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - SeedCorruptArchiveRelease, SeedMismatchedHashRelease, SeedMissingAssetRelease, SeedEmptyRelease helpers for emulate-backed resolver error tests
  - CreateBrokenRepo, CreateRepoMissingTag, CreateCorruptPackageRepo helpers for git-source resolver error tests
requires:
  - slice: S01
    provides: Emulate lifecycle, SeedPackageRelease pattern, git fixture CreateTaggedRepo foundation
affects:
  - S05
  - S06
key_files:
  - registry/testutil/emulatetest/seed.go
  - registry/testutil/emulatetest/emulatetest_test.go
  - registry/testutil/gitfixture/gitfixture.go
  - registry/testutil/gitfixture/gitfixture_test.go
key_decisions:
  - Reused shared internal pack-and-upload pipeline so all emulate failure helpers preserve the SeedPackageRelease contract.
  - Validated corrupt archive via exported packaging.LoadArchive API rather than internal package imports.
  - Kept missing-tag helper narrow so callers choose the absent tag they need.
  - Reused CreateTaggedRepo for all git failure fixtures to centralize git setup logic.
patterns_established:
  - Failure fixture pattern: each builder creates one specific failure precondition, returns enough context for downstream tests to exercise the error path without setup boilerplate
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S02/tasks/T01-SUMMARY.md
  - .gsd/milestones/M001-zku9aj/slices/S02/tasks/T02-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:35:58.797Z
blocker_discovered: false
---

# S02: Failure scenario test builders

**Failure-scenario seed helpers for emulate and git fixtures enabling downstream resolver error-path testing.**

## What Happened

Built on the S01 emulate lifecycle and git fixture foundations, this slice added seven failure-scenario builders across two packages. In emulatetest/seed.go, four new exported helpers were added: SeedCorruptArchiveRelease, SeedMismatchedHashRelease, SeedMissingAssetRelease, and SeedEmptyRelease. All reuse a shared internal pack-and-upload pipeline to stay aligned with the SeedPackageRelease contract while varying only the failure dimension. In gitfixture/gitfixture.go, three new helpers were added: CreateBrokenRepo, CreateRepoMissingTag, and CreateCorruptPackageRepo. All reuse CreateTaggedRepo internally so git setup logic stays centralized. Each helper has corresponding tests validating the intended failure precondition: corrupt archives fail LoadArchive, mismatched hashes disagree, missing-asset releases have exactly 1 asset, empty releases have 0 assets, broken repos lack the manifest file, missing tags are not found by git, and corrupt packages fail json.Unmarshal.

## Verification

Ran `go test ./registry/... -v -count=1 -timeout 60s` — all tests pass across both emulatetest and gitfixture packages. No regressions in S01 tests.

## Requirements Advanced

- R011 — Added failure-scenario builders (corrupt archives, mismatched hashes, missing assets, empty releases, broken git repos) completing the Builder API failure scenario coverage specified in R011

## Requirements Validated

None.

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

None.

## Known Limitations

None.

## Follow-ups

None.

## Files Created/Modified

- `registry/testutil/emulatetest/seed.go` — Added SeedCorruptArchiveRelease, SeedMismatchedHashRelease, SeedMissingAssetRelease, SeedEmptyRelease helpers and shared internal pipeline
- `registry/testutil/emulatetest/emulatetest_test.go` — Added tests for all four emulate failure scenarios
- `registry/testutil/gitfixture/gitfixture.go` — Added CreateBrokenRepo, CreateRepoMissingTag, CreateCorruptPackageRepo helpers
- `registry/testutil/gitfixture/gitfixture_test.go` — Added tests for all three git fixture failure scenarios
