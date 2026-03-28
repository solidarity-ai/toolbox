---
id: S01
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - emulatetest.Start(t) → *Server with BaseURL/Client for GitHub API testing
  - SeedPackageRelease() → creates repos with real .toolbox.pkg release assets
  - gitfixture.CreateTaggedRepo/CreateTaggedRepoFromDir → file:// cloneable repos at tags
  - CI workflow with Node.js 22 and emulate pre-installed
requires:
  []
affects:
  - S02
  - S05
  - S06
  - S07
  - S08
key_files:
  - registry/testutil/emulatetest/emulatetest.go
  - registry/testutil/emulatetest/seed.go
  - registry/testutil/emulatetest/emulatetest_test.go
  - registry/testutil/gitfixture/gitfixture.go
  - registry/testutil/gitfixture/gitfixture_test.go
  - .github/workflows/ci.yml
key_decisions:
  - Use real git CLI via exec.Command rather than a Go git library — tests exercise actual CI behavior
  - Singleton emulate subprocess per test binary via sync.Once — avoids port conflicts and startup overhead
  - Store uploaded asset bytes in SeedResult to work around emulate binary download limitation
patterns_established:
  - emulatetest.Start(t) singleton pattern for subprocess-backed test fixtures
  - gitfixture clone-based verification — create repo, clone to separate dir, verify from clone
  - Explicit git user config (-c user.name=test) for CI-safe fixture creation
observability_surfaces:
  - emulate stderr captured in buffer and logged on startup failure
  - HTTP seed errors include status code + response body
  - Test -v output shows emulate port, HTTP calls, and release metadata
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S01/tasks/T01-SUMMARY.md
  - .gsd/milestones/M001-zku9aj/slices/S01/tasks/T02-SUMMARY.md
  - .gsd/milestones/M001-zku9aj/slices/S01/tasks/T03-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-26T23:12:38.741Z
blocker_discovered: false
---

# S01: Emulate lifecycle + happy-path fixtures

**Built shared Go test infrastructure — emulate lifecycle manager, GitHub API seed builder, and local git fixture builder — that all downstream registry slices depend on.**

## What Happened

S01 delivered two independent test infrastructure packages and a CI update:

**emulatetest** (`registry/testutil/emulatetest/`): Manages a singleton `emulate` subprocess per test binary. `Start(t)` finds a free port, launches `npx emulate start --service github`, polls `/rate_limit` for readiness (30s timeout), and returns a `*Server` with `BaseURL()`, `Client()` (pre-authed via RoundTripper), and `Port()`. The `SeedClient` creates repos, releases, and uploads assets via emulate's GitHub REST API. `SeedPackageRelease` is the high-level helper — it calls `packaging.Pack()` to produce real `.toolbox.pkg` archives, then uploads both the archive and manifest as release assets. `SeedResult` retains uploaded bytes so S05 can work around emulate's broken binary download (documented in code). Gracefully skips via `t.Skip` when npx is unavailable.

**gitfixture** (`registry/testutil/gitfixture/`): Creates temporary git repos with tagged commits using real `git` CLI commands. `CreateTaggedRepo` writes arbitrary files; `CreateTaggedRepoFromDir` copies from an existing directory. `AddCommitAndTag` supports multi-tag histories. All repos use explicit git user config for CI compatibility. Tests verify by cloning to a separate temp dir and checking out tags.

**CI** (`.github/workflows/ci.yml`): Added `actions/setup-node@v4` (Node 22) and `npm install -g emulate` so emulate tests run in GitHub Actions instead of being skipped.

## Verification

Ran `go test ./registry/... -v -count=1 -timeout 60s` — all 6 tests across both packages passed. Verified CI config contains setup-node and emulate install steps. YAML validated via Ruby parser.

## Requirements Advanced

- R011 — Delivered emulate lifecycle manager, GitHub API seed builder with real .toolbox.pkg assets, git fixture builder, and CI integration. Happy-path coverage complete; failure scenarios deferred to S02.

## Requirements Validated

None.

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

T02 added an `AddCommitAndTag` helper not in the original plan, enabling cleaner multi-tag tests. T03 used Ruby YAML validation instead of Python (PyYAML not installed locally).

## Known Limitations

emulate v0.3.0 asset binary download returns JSON instead of binary — documented in code, workaround via SeedResult.ArchiveBytes for S05.

## Follow-ups

S02 will add failure scenario builders (corrupt archives, missing assets, broken git repos) on top of these helpers.

## Files Created/Modified

- `registry/testutil/emulatetest/emulatetest.go` — Emulate subprocess lifecycle manager — singleton start, health polling, auth client
- `registry/testutil/emulatetest/seed.go` — GitHub API seed builder — CreateRepo, CreateRelease, UploadReleaseAsset, SeedPackageRelease
- `registry/testutil/emulatetest/emulatetest_test.go` — Integration tests for emulate lifecycle and seeding
- `registry/testutil/gitfixture/gitfixture.go` — Git fixture builder — CreateTaggedRepo, CreateTaggedRepoFromDir, AddCommitAndTag
- `registry/testutil/gitfixture/gitfixture_test.go` — Tests verifying git fixtures via clone + checkout
- `.github/workflows/ci.yml` — Added setup-node@v4 (Node 22) and npm install -g emulate
