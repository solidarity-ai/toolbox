---
id: S05
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - PackageSource interface for S08 resolver orchestration
  - GitHubReleaseSource implementation for S08
  - ErrReleaseNotFound sentinel for S08 fallback logic
requires:
  - slice: S01
    provides: emulatetest.Start() and SeedPackageRelease helpers
  - slice: S02
    provides: SeedMissingAssetRelease and SeedEmptyRelease failure builders
  - slice: S03
    provides: ModulePath and Version types for Fetch signature
  - slice: S04
    provides: Cache layout design (not directly consumed yet but informed interface shape)
affects:
  - S06
  - S08
key_files:
  - registry/source.go
  - registry/source_test.go
key_decisions:
  - PackageSource interface with single Fetch method (archive+manifest bytes)
  - ErrReleaseNotFound sentinel for resolver fallback in S08
  - GITHUB_BASE_URL constructor param for emulate testing
patterns_established:
  - PackageSource interface pattern: Fetch(ctx, ModulePath, Version) → ([]byte, []byte, error) — S06 git-source will implement the same interface
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S05/tasks/T01-SUMMARY.md
  - .gsd/milestones/M001-zku9aj/slices/S05/tasks/T02-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-27T21:42:03.231Z
blocker_discovered: false
---

# S05: GitHub Releases source + PackageSource interface

**Defined the PackageSource interface and implemented GitHubReleaseSource with full emulate-backed integration tests for success and failure modes.**

## What Happened

This slice delivered two artifacts: the `PackageSource` interface in `registry/source.go` and its first implementation `GitHubReleaseSource`, which resolves packages from GitHub Release assets given a module path and version.\n\n**PackageSource interface** defines a single method: `Fetch(ctx, module, version) → (archive, manifest, error)`. This is the contract that S08's resolver orchestration will consume.\n\n**GitHubReleaseSource** derives owner/repo from module path segments, looks up releases by tag via the GitHub API, identifies `.toolbox.pkg` and `toolbox.pkg.json` assets, and downloads both with `Accept: application/octet-stream`. It supports `GITHUB_BASE_URL` override via the constructor for emulate testing. Error handling distinguishes release-not-found (sentinel `ErrReleaseNotFound`), missing archive/manifest assets, empty releases, and download failures.\n\n**Integration tests** in `registry/source_test.go` cover 5 cases against a real emulate subprocess: happy path, release not found, missing archive, missing manifest, and empty release. The happy-path test tolerates emulate's known limitation where asset downloads return JSON instead of binary, while still validating the full API traversal.

## Verification

Ran `go test ./registry -v -count=1 -run TestGitHubReleaseSource -timeout 60s` — all 5 subtests passed (happy_path, release_not_found, missing_archive_asset, missing_manifest_asset, empty_release). Build and vet clean.

## Requirements Advanced

- R003 — Implemented GitHubReleaseSource that fetches .toolbox.pkg and toolbox.pkg.json from GitHub Release assets with GITHUB_BASE_URL override, verified by 5 emulate-backed integration tests

## Requirements Validated

None.

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

None. T02 confirmed the T01-written tests already satisfied the full integration coverage plan, so no additional test code was needed.

## Known Limitations

Emulate's asset download endpoint returns JSON instead of raw binary bytes. The happy-path test tolerates this while verifying API traversal correctness. Real GitHub API returns binary — no product code workaround needed.

## Follow-ups

None.

## Files Created/Modified

- `registry/source.go` — PackageSource interface and GitHubReleaseSource implementation
- `registry/source_test.go` — 5 emulate-backed integration tests for GitHubReleaseSource
