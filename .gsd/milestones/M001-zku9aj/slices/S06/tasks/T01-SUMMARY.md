---
id: T01
parent: S06
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/git_source.go", "registry/git_source_test.go", ".gsd/milestones/M001-zku9aj/slices/S06/tasks/T01-SUMMARY.md"]
key_decisions: ["Added a URLPrefix override on GitSourceFallback so tests can clone local repositories via file:// while production defaults to https://.", "Included module, tag, clone URL, and captured git output in git clone failure errors for direct debugging context."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s`; it passed."
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T01: Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.

> Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.

## What Happened
---
id: T01
parent: S06
milestone: M001-zku9aj
key_files:
  - registry/git_source.go
  - registry/git_source_test.go
  - .gsd/milestones/M001-zku9aj/slices/S06/tasks/T01-SUMMARY.md
key_decisions:
  - Added a URLPrefix override on GitSourceFallback so tests can clone local repositories via file:// while production defaults to https://.
  - Included module, tag, clone URL, and captured git output in git clone failure errors for direct debugging context.
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:34:32.572Z
blocker_discovered: false
---

# T01: Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.

**Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.**

## What Happened

Implemented registry/git_source.go with GitSourceFallback, a PackageSource implementation that defaults clone URLs to https:// but supports an injected URLPrefix for file:// tests. Fetch now creates isolated temp directories, shallow-clones the requested version tag, packages the checkout with packaging.Pack, reads the archive and manifest bytes, and cleans up temporary state. Added registry/git_source_test.go with TestGitSource/happy_path using a tagged calc fixture repository and verified that Fetch returns non-empty archive and manifest bytes. During verification, corrected the test to use the existing fixture discovery helper because go test runs from the registry package directory rather than the repository root.

## Verification

Ran `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s`; it passed.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s` | 0 | ✅ pass | 1500ms |


## Deviations

Used the existing fixtureSourceDir helper instead of a hard-coded relative fixture path so the test resolves fixtures correctly from the registry package working directory.

## Known Issues

None.

## Files Created/Modified

- `registry/git_source.go`
- `registry/git_source_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S06/tasks/T01-SUMMARY.md`


## Deviations
Used the existing fixtureSourceDir helper instead of a hard-coded relative fixture path so the test resolves fixtures correctly from the registry package working directory.

## Known Issues
None.
