---
id: T02
parent: S02
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/testutil/gitfixture/gitfixture.go", "registry/testutil/gitfixture/gitfixture_test.go", ".gsd/milestones/M001-zku9aj/slices/S02/tasks/T02-SUMMARY.md"]
key_decisions: ["Kept the missing-tag helper focused on creating a valid repository with only the existing tag so downstream tests can supply whichever absent tag they need.", "Reused CreateTaggedRepo for all failure fixtures so scenario differences stay limited to repository contents instead of duplicating git setup logic."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran gofmt on the edited files, then ran `go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 60s` and `go test ./registry/... -v -count=1 -timeout 60s`; both passed. Also attempted an LSP diagnostics check for registry/testutil/gitfixture/gitfixture.go, but no Go language server was available in this environment."
completed_at: 2026-03-27T10:07:33.399Z
blocker_discovered: false
---

# T02: Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.

> Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.

## What Happened
---
id: T02
parent: S02
milestone: M001-zku9aj
key_files:
  - registry/testutil/gitfixture/gitfixture.go
  - registry/testutil/gitfixture/gitfixture_test.go
  - .gsd/milestones/M001-zku9aj/slices/S02/tasks/T02-SUMMARY.md
key_decisions:
  - Kept the missing-tag helper focused on creating a valid repository with only the existing tag so downstream tests can supply whichever absent tag they need.
  - Reused CreateTaggedRepo for all failure fixtures so scenario differences stay limited to repository contents instead of duplicating git setup logic.
duration: ""
verification_result: passed
completed_at: 2026-03-27T10:07:33.409Z
blocker_discovered: false
---

# T02: Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.

**Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.**

## What Happened

Extended registry/testutil/gitfixture/gitfixture.go with CreateBrokenRepo, CreateRepoMissingTag, and CreateCorruptPackageRepo, all built on the existing temporary repo, shared git initialization, and commit-and-tag flow. Added tests in registry/testutil/gitfixture/gitfixture_test.go that clone each fixture and verify the intended failure preconditions: no toolbox.devpkg.json for broken repos, no matching git tag for the missing-tag scenario, and malformed toolbox.devpkg.json that fails json.Unmarshal for the corrupt-package scenario. Kept the missing-tag helper intentionally narrow so downstream resolver tests choose the absent tag they want to request while the fixture only guarantees a valid repo tagged at the existing version.

## Verification

Ran gofmt on the edited files, then ran `go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 60s` and `go test ./registry/... -v -count=1 -timeout 60s`; both passed. Also attempted an LSP diagnostics check for registry/testutil/gitfixture/gitfixture.go, but no Go language server was available in this environment.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 60s` | 0 | ✅ pass | 1233ms |
| 2 | `go test ./registry/... -v -count=1 -timeout 60s` | 0 | ✅ pass | 4614ms |


## Deviations

None.

## Known Issues

None.

## Files Created/Modified

- `registry/testutil/gitfixture/gitfixture.go`
- `registry/testutil/gitfixture/gitfixture_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S02/tasks/T02-SUMMARY.md`


## Deviations
None.

## Known Issues
None.
