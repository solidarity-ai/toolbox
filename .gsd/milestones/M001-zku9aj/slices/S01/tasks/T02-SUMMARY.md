---
id: T02
parent: S01
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/testutil/gitfixture/gitfixture.go", "registry/testutil/gitfixture/gitfixture_test.go"]
key_decisions: ["Keep the git fixture helper on top of exec.Command("git", ...) so tests exercise the real CLI behavior and CI git config requirements.", "Add a small AddCommitAndTag helper so multi-tag history assertions stay in-package and downstream slices can reuse the same real-git flow."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s from the worktree root. All three gitfixture tests passed, with output showing repo creation, clone paths, tag discovery, and file verification after checkout. Ran the slice-level shared infrastructure check go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s after the new gitfixture package landed. The existing emulate lifecycle suite still passed, showing emulate startup on a port, successful HTTP calls, and release metadata verification."
completed_at: 2026-03-26T22:38:47.000Z
blocker_discovered: false
---

# T02: Built local tagged git fixture helpers with clone-based verification.

> Built local tagged git fixture helpers with clone-based verification.

## What Happened
---
id: T02
parent: S01
milestone: M001-zku9aj
key_files:
  - registry/testutil/gitfixture/gitfixture.go
  - registry/testutil/gitfixture/gitfixture_test.go
key_decisions:
  - Keep the git fixture helper on top of exec.Command("git", ...) so tests exercise the real CLI behavior and CI git config requirements.
  - Add a small AddCommitAndTag helper so multi-tag history assertions stay in-package and downstream slices can reuse the same real-git flow.
duration: ""
verification_result: passed
completed_at: 2026-03-26T22:38:47.079Z
blocker_discovered: false
---

# T02: Built local tagged git fixture helpers with clone-based verification.

**Built local tagged git fixture helpers with clone-based verification.**

## What Happened

Added the new registry/testutil/gitfixture package for creating temporary non-bare git repositories backed by the real git CLI. CreateTaggedRepo initializes a repo, writes arbitrary files, commits with explicit test user config for CI, and tags the commit. CreateTaggedRepoFromDir copies a real fixture directory into a temp repo with filepath.WalkDir, commits it, and tags it. I also added AddCommitAndTag so tests can build multi-tag histories against the same repo without hand-rolling git commands in each test. The tests verify the helpers the same way downstream slices will use them: clone the generated repo into a separate temp directory, inspect tags from the clone, check out tagged revisions, and read real files from disk. Coverage includes a one-file repo, a repo built from the calc fixture source directory, and a two-tag history where the checked-out file contents differ across tags.

## Verification

Ran go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s from the worktree root. All three gitfixture tests passed, with output showing repo creation, clone paths, tag discovery, and file verification after checkout. Ran the slice-level shared infrastructure check go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s after the new gitfixture package landed. The existing emulate lifecycle suite still passed, showing emulate startup on a port, successful HTTP calls, and release metadata verification.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s` | 0 | ✅ pass | 127ms |
| 2 | `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` | 0 | ✅ pass | 4264ms |


## Deviations

Added an AddCommitAndTag helper in gitfixture.go to keep the multi-tag test on the real git path without duplicating commit boilerplate in the test body.

## Known Issues

None.

## Files Created/Modified

- `registry/testutil/gitfixture/gitfixture.go`
- `registry/testutil/gitfixture/gitfixture_test.go`


## Deviations
Added an AddCommitAndTag helper in gitfixture.go to keep the multi-tag test on the real git path without duplicating commit boilerplate in the test body.

## Known Issues
None.
