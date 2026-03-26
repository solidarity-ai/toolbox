---
id: T01
parent: S01
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["registry/testutil/emulatetest/emulatetest.go", "registry/testutil/emulatetest/seed.go", "registry/testutil/emulatetest/emulatetest_test.go", "registry/testutil/emulatetest/sysproc_linux.go", "registry/testutil/emulatetest/sysproc_other.go"]
key_decisions: ["Use POST /user/repos and the auto-created admin user as the fixture owner because emulate v0.3.0 returns 404 for org creation endpoints.", "Keep the emulate subprocess shared per test binary with sync.Once and shut it down in TestMain so lifecycle tests reuse one server without leaking processes."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Ran go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s from the worktree root. The suite passed end-to-end, logging emulate startup on a chosen port, successful /rate_limit checks, repo and release creation, package asset uploads, release-by-tag metadata verification, and descriptive 404 handling for missing repos."
completed_at: 2026-03-26T22:19:54.544Z
blocker_discovered: false
---

# T01: Added shared emulate lifecycle and GitHub release seeding helpers with integration coverage for the real emulator API.

> Added shared emulate lifecycle and GitHub release seeding helpers with integration coverage for the real emulator API.

## What Happened
---
id: T01
parent: S01
milestone: M001-zku9aj
key_files:
  - registry/testutil/emulatetest/emulatetest.go
  - registry/testutil/emulatetest/seed.go
  - registry/testutil/emulatetest/emulatetest_test.go
  - registry/testutil/emulatetest/sysproc_linux.go
  - registry/testutil/emulatetest/sysproc_other.go
key_decisions:
  - Use POST /user/repos and the auto-created admin user as the fixture owner because emulate v0.3.0 returns 404 for org creation endpoints.
  - Keep the emulate subprocess shared per test binary with sync.Once and shut it down in TestMain so lifecycle tests reuse one server without leaking processes.
duration: ""
verification_result: passed
completed_at: 2026-03-26T22:19:54.593Z
blocker_discovered: false
---

# T01: Added shared emulate lifecycle and GitHub release seeding helpers with integration coverage for the real emulator API.

**Added shared emulate lifecycle and GitHub release seeding helpers with integration coverage for the real emulator API.**

## What Happened

Built the new registry/testutil/emulatetest package for shared emulate-backed registry tests. The lifecycle manager starts npx emulate exactly once per test binary, waits for /rate_limit readiness on a dynamically chosen port, exposes an authenticated HTTP client, captures subprocess output for startup diagnostics, and cleans the process up at test-binary exit. The seed client now creates repos, releases, and release assets against the real emulate GitHub API, and SeedPackageRelease packs the calc fixture with packaging.Pack, uploads both generated artifacts, and returns the raw uploaded bytes for downstream slices. Integration coverage now proves the full happy path plus the required missing-source and missing-repo failure modes.

## Verification

Ran go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s from the worktree root. The suite passed end-to-end, logging emulate startup on a chosen port, successful /rate_limit checks, repo and release creation, package asset uploads, release-by-tag metadata verification, and descriptive 404 handling for missing repos.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` | 0 | ✅ pass | 4456ms |


## Deviations

Added Linux/non-Linux platform helper files for Pdeathsig portability, used os.MkdirTemp inside SeedPackageRelease because the planned method signature had no testing.T, and implemented repo creation via /user/repos after confirming emulate v0.3.0 does not expose the org creation endpoints.

## Known Issues

Emulate v0.3.0 still does not reliably return uploaded release asset bytes from the download path, so SeedResult preserves the uploaded archive and manifest bytes for downstream workaround code.

## Files Created/Modified

- `registry/testutil/emulatetest/emulatetest.go`
- `registry/testutil/emulatetest/seed.go`
- `registry/testutil/emulatetest/emulatetest_test.go`
- `registry/testutil/emulatetest/sysproc_linux.go`
- `registry/testutil/emulatetest/sysproc_other.go`


## Deviations
Added Linux/non-Linux platform helper files for Pdeathsig portability, used os.MkdirTemp inside SeedPackageRelease because the planned method signature had no testing.T, and implemented repo creation via /user/repos after confirming emulate v0.3.0 does not expose the org creation endpoints.

## Known Issues
Emulate v0.3.0 still does not reliably return uploaded release asset bytes from the download path, so SeedResult preserves the uploaded archive and manifest bytes for downstream workaround code.
