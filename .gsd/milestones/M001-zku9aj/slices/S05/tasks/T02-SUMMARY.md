---
id: T02
parent: S05
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/source_test.go", ".gsd/KNOWLEDGE.md", ".gsd/milestones/M001-zku9aj/slices/S05/tasks/T02-SUMMARY.md"]
key_decisions: ["Accepted the existing `registry/source_test.go` suite as the T02 deliverable because it already implements the full planned emulate-backed integration coverage.", "Recorded the worktree-specific `go test` invocation workaround in `.gsd/KNOWLEDGE.md` instead of changing product code, because the failure mode was environmental rather than a defect in `GitHubReleaseSource` or its tests."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitHubReleaseSource -timeout 60s` and `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/testutil/emulatetest -v -count=1 -run 'TestSeed(PackageRelease|MissingAssetRelease|EmptyRelease)$' -timeout 60s`; both passed."
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T02: Verified the existing emulate-backed GitHubReleaseSource integration suite and documented the worktree-safe Go test invocation needed to run it reliably here.

> Verified the existing emulate-backed GitHubReleaseSource integration suite and documented the worktree-safe Go test invocation needed to run it reliably here.

## What Happened
---
id: T02
parent: S05
milestone: M001-zku9aj
key_files:
  - registry/source_test.go
  - .gsd/KNOWLEDGE.md
  - .gsd/milestones/M001-zku9aj/slices/S05/tasks/T02-SUMMARY.md
key_decisions:
  - Accepted the existing `registry/source_test.go` suite as the T02 deliverable because it already implements the full planned emulate-backed integration coverage.
  - Recorded the worktree-specific `go test` invocation workaround in `.gsd/KNOWLEDGE.md` instead of changing product code, because the failure mode was environmental rather than a defect in `GitHubReleaseSource` or its tests.
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T02: Verified the existing emulate-backed GitHubReleaseSource integration suite and documented the worktree-safe Go test invocation needed to run it reliably here.

**Verified the existing emulate-backed GitHubReleaseSource integration suite and documented the worktree-safe Go test invocation needed to run it reliably here.**

## What Happened

Validated the T02 contract against the current repository and confirmed `registry/source_test.go` already contains the required emulate-backed integration coverage for the happy path, release-not-found, missing archive, missing manifest, and empty release cases. Verified that the local fixture path differs from the planner snapshot: the repository uses the shared `testutil/fixtures/toolbox.pkgs/calc` source fixture via `fixtures.SourceDirs()` instead of `registry/testutil/emulatetest/testdata/calc-pkg`. During verification, discovered a worktree-specific Go package resolution issue where plain `go test` arguments can resolve through the linked `/home/mackross/dev/toolbox` checkout instead of the active `.gsd` worktree, so I recorded the successful `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ...` invocation in `.gsd/KNOWLEDGE.md`. No product code changes were required because the shipped tests already satisfied the task contract.

## Verification

Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitHubReleaseSource -timeout 60s` and `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/testutil/emulatetest -v -count=1 -run 'TestSeed(PackageRelease|MissingAssetRelease|EmptyRelease)$' -timeout 60s`; both passed.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitHubReleaseSource -timeout 60s` | 0 | ✅ pass | 1473ms |
| 2 | `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/testutil/emulatetest -v -count=1 -run 'TestSeed(PackageRelease|MissingAssetRelease|EmptyRelease)$' -timeout 60s` | 0 | ✅ pass | 3461ms |


## Deviations

No shipped code changes were necessary because the existing integration suite already matched the T02 plan. The only local adaptation was using the repository's real shared `calc` fixture lookup instead of the stale `calc-pkg` path from the plan snapshot, plus documenting the worktree-specific Go invocation workaround in knowledge.

## Known Issues

Emulate still has the known release asset download limitation where binary asset fetches may return JSON instead of the uploaded binary payload. The happy-path test intentionally tolerates that behavior while still validating release lookup, asset identification, and non-empty downloads.

## Files Created/Modified

- `registry/source_test.go`
- `.gsd/KNOWLEDGE.md`
- `.gsd/milestones/M001-zku9aj/slices/S05/tasks/T02-SUMMARY.md`


## Deviations
No shipped code changes were necessary because the existing integration suite already matched the T02 plan. The only local adaptation was using the repository's real shared `calc` fixture lookup instead of the stale `calc-pkg` path from the plan snapshot, plus documenting the worktree-specific Go invocation workaround in knowledge.

## Known Issues
Emulate still has the known release asset download limitation where binary asset fetches may return JSON instead of the uploaded binary payload. The happy-path test intentionally tolerates that behavior while still validating release lookup, asset identification, and non-empty downloads.
