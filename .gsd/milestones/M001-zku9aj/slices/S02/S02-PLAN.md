# S02: 

**Goal:** ---
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

**Demo:** After this: # S02: Failure scenario test builders — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-28T00:35:58.797Z

# S02: Failure scenario test builders — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: All deliverables are test helpers verified by their own unit tests — no runtime behavior.

## Preconditions

- Node.js and npx available on PATH (for emulate)
- Git available on PATH
- Working directory: the M001-zku9aj worktree root

## Smoke Test

Run `go test ./registry/... -v -count=1 -timeout 60s` — all tests pass with no regressions.

## Test Cases

### 1. Corrupt archive release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedCorruptArchiveRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. SeedResult.ArchiveBytes are random garbage. `packaging.LoadArchive` fails on those bytes.

### 2. Mismatched hash release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedMismatchedHashRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Manifest sha256 does not match the sha256 of the actual archive bytes.

### 3. Missing asset release (archive missing)

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedMissingAssetRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Release has exactly 1 asset. Mode "archive" produces manifest-only; mode "manifest" produces archive-only.

### 4. Empty release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedEmptyRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Release has 0 assets.

### 5. Broken git repo (no manifest)

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateBrokenRepo -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Cloned repo has no `toolbox.devpkg.json`.

### 6. Missing git tag

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateRepoMissingTag -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Repo has the existing tag but `git tag -l` for the missing tag returns empty.

### 7. Corrupt package repo

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateCorruptPackageRepo -v -count=1 -timeout 30s`
2. **Expected:** Test passes. `toolbox.devpkg.json` exists but `json.Unmarshal` fails on its contents.

## Edge Cases

### npx not available

1. Remove npx from PATH and run emulate tests.
2. **Expected:** Emulate tests skip with "emulatetest: npx not available" rather than fail.

## Failure Signals

- Any test in `go test ./registry/... -timeout 60s` failing
- Compilation errors in seed.go or gitfixture.go

## Not Proven By This UAT

- Downstream resolver behavior when encountering these failure scenarios (covered by S05, S06)
- CI execution (emulate tests require npx; CI setup validated in S01)

## Notes for Tester

All emulate tests require a ~2s startup for the emulate server. The gitfixture tests are fast (~0.2s total).



## Tasks
- [x] **T01: Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.** — 
- [x] **T02: Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.** — 
