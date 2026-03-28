---
id: T02
parent: S06
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/git_source_test.go", ".gsd/milestones/M001-zku9aj/slices/S06/tasks/T02-SUMMARY.md"]
key_decisions: ["Asserted failure shape by subsystem boundary: missing tags must fail at git clone, while broken and corrupt packages must fail at the packaging step."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource -timeout 30s` and `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/... -v -count=1 -timeout 60s`; both passed."
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T02: Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.

> Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.

## What Happened
---
id: T02
parent: S06
milestone: M001-zku9aj
key_files:
  - registry/git_source_test.go
  - .gsd/milestones/M001-zku9aj/slices/S06/tasks/T02-SUMMARY.md
key_decisions:
  - Asserted failure shape by subsystem boundary: missing tags must fail at git clone, while broken and corrupt packages must fail at the packaging step.
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T02: Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.

**Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.**

## What Happened

Extended registry/git_source_test.go with three new TestGitSource subtests covering the planned failure boundaries. The missing_tag case uses gitfixture.CreateRepoMissingTag and verifies Fetch fails when the requested tag does not exist. The broken_repo case uses gitfixture.CreateBrokenRepo and verifies Fetch fails during packaging when toolbox.devpkg.json is absent. The corrupt_package case uses gitfixture.CreateCorruptPackageRepo and verifies malformed toolbox.devpkg.json also fails during packaging. All cases use GitSourceFallback with URLPrefix set to file:// so the tests clone local temporary repos created by the fixtures.

## Verification

Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource -timeout 30s` and `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/... -v -count=1 -timeout 60s`; both passed.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource -timeout 30s` | 0 | ✅ pass | 1647ms |
| 2 | `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/... -v -count=1 -timeout 60s` | 0 | ✅ pass | 4946ms |


## Deviations

None.

## Known Issues

No Go language server was available in this worktree, so verification used the passing Go test suite rather than LSP diagnostics.

## Files Created/Modified

- `registry/git_source_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S06/tasks/T02-SUMMARY.md`


## Deviations
None.

## Known Issues
No Go language server was available in this worktree, so verification used the passing Go test suite rather than LSP diagnostics.
