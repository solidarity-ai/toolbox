# S06: 

**Goal:** ---
id: S06
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - GitSourceFallback implementing PackageSource for S08 resolver orchestration
requires:
  - slice: S01
    provides: gitfixture helpers for creating tagged repos
  - slice: S02
    provides: CreateBrokenRepo, CreateRepoMissingTag, CreateCorruptPackageRepo failure fixtures
  - slice: S03
    provides: ModulePath and Version types for Fetch signature
  - slice: S04
    provides: Cache layout design (not directly consumed but informed interface shape)
affects:
  - S07
  - S08
key_files:
  - registry/git_source.go
  - registry/git_source_test.go
key_decisions:
  - URLPrefix override on GitSourceFallback for file:// test cloning vs https:// production
  - Error assertions by subsystem boundary: missing tag fails at git clone, broken/corrupt packages fail at packaging step
patterns_established:
  - Second PackageSource implementation confirms the interface is stable — S08 can compose both without interface changes
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S06/tasks/T01-SUMMARY.md
  - .gsd/milestones/M001-zku9aj/slices/S06/tasks/T02-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:36:53.060Z
blocker_discovered: false
---

# S06: Git-source fallback resolver

**Implemented GitSourceFallback as a second PackageSource that clones tagged git repos, packages them locally via packaging.Pack, and returns archive+manifest bytes — with 4 tests covering happy path and 3 failure modes.**

## What Happened

This slice delivered GitSourceFallback in registry/git_source.go, a PackageSource implementation that resolves packages directly from git when no GitHub Release assets exist. It shallow-clones the repo at the requested version tag, runs packaging.Pack on the checkout, reads the resulting archive and manifest bytes, and cleans up temp directories. T01 built the core implementation and happy-path test. GitSourceFallback has a single configurable field URLPrefix — defaults to https:// for production, overridden to file:// in tests to clone local fixture repos. The happy-path test uses gitfixture.CreateTaggedRepoFromDir with the calc fixture to verify Fetch returns non-empty archive and manifest bytes. T02 added three error-path tests: missing_tag, broken_repo, and corrupt_package. Error assertions validate failure at the correct subsystem boundary — git vs packaging.

## Verification

Ran `go test ./registry -v -count=1 -run TestGitSource -timeout 30s` and full regression `go test ./registry/... -v -count=1 -timeout 60s`; both passed.

## Requirements Advanced

- R004 — GitSourceFallback clones tagged repos, packages them with packaging.Pack, and returns archive+manifest bytes through the same PackageSource interface as GitHubReleaseSource

## Requirements Validated

- R004 — 4 tests prove clone+pack flow for valid repos and correct failure handling for missing tags, missing manifests, and corrupt manifests

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

None. Both tasks executed as planned.

## Known Limitations

No auth support for private git repos (deferred to S08/S12 along with GitHub token handling). No caching of git-source results within GitSourceFallback itself (S08 resolver orchestration handles cache population).

## Follow-ups

None.

## Files Created/Modified

- `registry/git_source.go` — GitSourceFallback implementing PackageSource via git clone + packaging.Pack
- `registry/git_source_test.go` — 4 subtests: happy_path, missing_tag, broken_repo, corrupt_package

**Demo:** After this: # S06: Git-source fallback resolver — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-28T00:36:53.060Z

# S06: Git-source fallback resolver — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T22:30:00.000Z

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: Library code with no runtime behavior — validated entirely by unit tests against local git fixture repos.

## Preconditions

- Git available on PATH
- Working directory: the M001-zku9aj worktree root

## Smoke Test

Run `go test ./registry -v -count=1 -run TestGitSource -timeout 30s` — all 4 subtests pass.

## Test Cases

### 1. Happy path — clone tagged repo, pack, return bytes

1. Run `go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s`
2. **Expected:** Passes. Fetch returns non-empty archive and manifest bytes from a calc fixture repo tagged v1.0.0.

### 2. Missing tag — absent version fails at git clone

1. Run `go test ./registry -v -count=1 -run TestGitSource/missing_tag -timeout 30s`
2. **Expected:** Passes. Fetching version v9.9.9 from a repo that only has v1.0.0 returns a non-nil error. Error originates from git clone, not packaging.

### 3. Broken repo — no package manifest fails at packaging

1. Run `go test ./registry -v -count=1 -run TestGitSource/broken_repo -timeout 30s`
2. **Expected:** Passes. Repo clones successfully but has no toolbox.devpkg.json. Error originates from packaging.Pack.

### 4. Corrupt package — malformed manifest fails at packaging

1. Run `go test ./registry -v -count=1 -run TestGitSource/corrupt_package -timeout 30s`
2. **Expected:** Passes. Repo clones successfully but toolbox.devpkg.json contains invalid JSON. Error originates from packaging.Pack.

## Edge Cases

### Full regression suite

1. Run `go test ./registry/... -v -count=1 -timeout 60s`
2. **Expected:** All tests pass across registry, emulatetest, and gitfixture packages with no regressions.

### Git not on PATH

1. If git is removed from PATH, GitSourceFallback.Fetch returns an exec error rather than panicking.
2. **Expected:** Non-nil error with exec context, no panic.

## Failure Signals

- Any subtest in `go test ./registry -run TestGitSource` failing
- Compilation errors in git_source.go
- Regressions in existing Cache or GitHubReleaseSource tests

## Not Proven By This UAT

- Auth token handling for private git repos (deferred to S08/S12)
- Cache population after fetch (S08 resolver orchestration responsibility)
- Pseudo-version resolution via git refs (S07)
- Integration with resolver orchestration fallback logic (S08)

## Notes for Tester

Tests run in ~0.13s total. No network access required — all tests clone local file:// repos created by gitfixture helpers. No Node.js/npx dependency (unlike S05's emulate tests).



## Tasks
- [x] **T01: Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.** — 
- [x] **T02: Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.** — 
