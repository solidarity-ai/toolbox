---
id: T01
parent: S05
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/source.go", "registry/source_test.go", ".gsd/milestones/M001-zku9aj/slices/S05/tasks/T01-SUMMARY.md"]
key_decisions: ["Exposed ErrReleaseNotFound as a package-level sentinel so callers and tests can distinguish missing releases from other HTTP/download failures.", "Separated release metadata lookup from asset downloads to keep GitHubReleaseSource failure reporting contextual and easy to test."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s and confirmed all five source subtests passed against a real emulate subprocess. Ran go build ./registry/... && go vet ./registry/... and both completed successfully after formatting the new Go files with gofmt."
completed_at: 2026-03-27T21:24:42.640Z
blocker_discovered: false
---

# T01: Added the PackageSource interface and a GitHubReleaseSource with emulate-backed integration tests for success and release/asset failure modes.

> Added the PackageSource interface and a GitHubReleaseSource with emulate-backed integration tests for success and release/asset failure modes.

## What Happened
---
id: T01
parent: S05
milestone: M001-zku9aj
key_files:
  - registry/source.go
  - registry/source_test.go
  - .gsd/milestones/M001-zku9aj/slices/S05/tasks/T01-SUMMARY.md
key_decisions:
  - Exposed ErrReleaseNotFound as a package-level sentinel so callers and tests can distinguish missing releases from other HTTP/download failures.
  - Separated release metadata lookup from asset downloads to keep GitHubReleaseSource failure reporting contextual and easy to test.
duration: ""
verification_result: passed
completed_at: 2026-03-27T21:24:42.648Z
blocker_discovered: false
---

# T01: Added the PackageSource interface and a GitHubReleaseSource with emulate-backed integration tests for success and release/asset failure modes.

**Added the PackageSource interface and a GitHubReleaseSource with emulate-backed integration tests for success and release/asset failure modes.**

## What Happened

Implemented registry/source.go with the new PackageSource interface and a GitHubReleaseSource that derives owner/repo from module paths, defaults to the GitHub API base URL, fetches releases by tag, locates the .toolbox.pkg archive and toolbox.pkg.json manifest assets, and downloads both assets with application/octet-stream requests. Added ErrReleaseNotFound for 404 release lookups plus descriptive errors for invalid module paths, empty releases, missing archive assets, missing manifest assets, and asset download failures. Added registry/source_test.go with emulate-backed integration coverage for happy path, release not found, missing archive, missing manifest, and empty release cases, including tolerance for the known emulate binary-download limitation documented in the slice plan.

## Verification

Ran go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s and confirmed all five source subtests passed against a real emulate subprocess. Ran go build ./registry/... && go vet ./registry/... and both completed successfully after formatting the new Go files with gofmt.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s` | 0 | ✅ pass | 2575ms |
| 2 | `go build ./registry/... && go vet ./registry/...` | 0 | ✅ pass | 157ms |


## Deviations

None.

## Known Issues

Emulate still exhibits the known asset-download limitation where release asset downloads may return JSON rather than the raw uploaded binary bytes. The happy-path integration test accounts for this and still verifies the API traversal and asset selection behavior.

## Files Created/Modified

- `registry/source.go`
- `registry/source_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S05/tasks/T01-SUMMARY.md`


## Deviations
None.

## Known Issues
Emulate still exhibits the known asset-download limitation where release asset downloads may return JSON rather than the raw uploaded binary bytes. The happy-path integration test accounts for this and still verifies the API traversal and asset selection behavior.
