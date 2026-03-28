# S02: Failure scenario test builders — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T10:57:47.642Z

# S02: Failure scenario test builders — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: All deliverables are test helpers verified by their own unit tests — no runtime behavior.

## Preconditions

- Node.js and npx available on PATH (for emulate)
- Git available on PATH
- Working directory: the M001-zku9aj worktree root

## Smoke Test

Run `go test ./registry/... -v -count=1 -timeout 60s` — all tests pass with no regressions.

## Test Cases

### 1. Corrupt archive release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedCorruptArchiveRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. SeedResult.ArchiveBytes are random garbage. `packaging.LoadArchive` fails on those bytes.

### 2. Mismatched hash release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedMismatchedHashRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Manifest sha256 does not match the sha256 of the actual archive bytes.

### 3. Missing asset release (archive missing)

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedMissingAssetRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Release has exactly 1 asset. Mode "archive" produces manifest-only; mode "manifest" produces archive-only.

### 4. Empty release

1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedEmptyRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Release has 0 assets.

### 5. Broken git repo (no manifest)

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateBrokenRepo -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Cloned repo has no `toolbox.devpkg.json`.

### 6. Missing git tag

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateRepoMissingTag -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Repo has the existing tag but `git tag -l` for the missing tag returns empty.

### 7. Corrupt package repo

1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateCorruptPackageRepo -v -count=1 -timeout 30s`
2. **Expected:** Test passes. `toolbox.devpkg.json` exists but `json.Unmarshal` fails on its contents.

## Edge Cases

### npx not available

1. Remove npx from PATH and run emulate tests.
2. **Expected:** Emulate tests skip with "emulatetest: npx not available" rather than fail.

## Failure Signals

- Any test in `go test ./registry/... -timeout 60s` failing
- Compilation errors in seed.go or gitfixture.go

## Not Proven By This UAT

- Downstream resolver behavior when encountering these failure scenarios (covered by S05, S06)
- CI execution (emulate tests require npx; CI setup validated in S01)

## Notes for Tester

All emulate tests require a ~2s startup for the emulate server. The gitfixture tests are fast (~0.2s total).
