# M001-zku9aj: M001-zku9aj: M001-zku9aj: Tool Registry, FQN, and Auto-Download — Context

## Vision
M001-zku9aj: M001-zku9aj: Tool Registry, FQN, and Auto-Download — Context

## Slice Overview
| ID | Slice | Risk | Depends | Done | After this |
|----|-------|------|---------|------|------------|
| S01 | Emulate lifecycle + happy-path fixtures | high | — | ✅ | # S01: Emulate lifecycle + happy-path fixtures — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-26T23:12:38.741Z

# S01 UAT: Emulate lifecycle + happy-path fixtures

## Preconditions
- Node.js and npx available on PATH
- Git available on PATH
- Working directory: the M001-zku9aj worktree root

## Test 1: Emulate lifecycle starts and responds
1. Run `go test ./registry/testutil/emulatetest/ -run TestEmulateLifecycle -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Output shows emulate starting on a dynamic port. `GET /rate_limit` returns 200.

## Test 2: Seed repo and release via emulate API
1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedRepoAndRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Repo created (201), release created with tag "v1.0.0", release ID > 0.

## Test 3: Seed full package release with real archives
1. Run `go test ./registry/testutil/emulatetest/ -run TestSeedPackageRelease -v -count=1 -timeout 60s`
2. **Expected:** Test passes. Release at tag "v1.0.0" has 2 assets: `calc.toolbox.pkg` and `toolbox.pkg.json`, both with size > 0.

## Test 4: Git fixture — single tagged repo
1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateTaggedRepo -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Repo cloned to separate dir, tag v1.0.0 found, hello.txt contains "hello".

## Test 5: Git fixture — repo from fixture directory
1. Run `go test ./registry/testutil/gitfixture/ -run TestCreateTaggedRepoFromDir -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Tag v2.0.0 checked out, `toolbox.devpkg.json` exists and contains `"name": "calc"`.

## Test 6: Git fixture — multiple tags
1. Run `go test ./registry/testutil/gitfixture/ -run TestMultipleTags -v -count=1 -timeout 30s`
2. **Expected:** Test passes. Both v1.0.0 and v2.0.0 tags exist. File contents differ between tags.

## Test 7: Full registry test suite
1. Run `go test ./registry/... -v -count=1 -timeout 60s`
2. **Expected:** All tests pass across both packages with no port conflicts.

## Test 8: CI workflow validity
1. Run `grep -q 'setup-node' .github/workflows/ci.yml && grep -q 'npm install -g emulate' .github/workflows/ci.yml && echo PASS`
2. **Expected:** Prints PASS. CI includes Node.js setup and emulate pre-install.

## Edge case: npx not available
1. If npx is removed from PATH, emulate tests should skip with message "emulatetest: npx not available" rather than fail.

 |
| S02 | Failure scenario test builders | medium | S01 | ✅ | # S02: Failure scenario test builders — UAT

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
 |
| S03 | FQN types and parsing | low | — | ✅ | # S03: FQN types and parsing — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T15:45:08.070Z

# S03: FQN types and parsing — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: Pure types and parsers with no runtime behavior — validated entirely by unit tests.

## Preconditions

- Working directory: the M001-zku9aj worktree root
- Go toolchain available

## Smoke Test

Run `go test ./tool/... -v -count=1 -run TestFQN` — all tests pass.

## Test Cases

### 1. ModulePath parsing accepts valid host/path

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseModulePath/valid`
2. **Expected:** Passes. `github.com/owner/repo` parses successfully. `String()` returns the same value.

### 2. ModulePath rejects single-segment or no-dot paths

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseModulePath/no_dot`
2. **Expected:** Passes. Inputs like `nohost/repo` or `justasegment` return errors.

### 3. Version parsing accepts semver with prerelease and build

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/valid_semver_prerelease_build`
2. **Expected:** Passes. `v1.2.3-alpha.1+build` parses and round-trips.

### 4. Pseudo-version parsing and decomposition

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/valid_pseudo`
2. **Expected:** Passes. `v0.0.0-20260101120000-abcdef012345` parses. `IsPseudo()` returns true. `PseudoTimestamp()` returns `20260101120000`. `PseudoCommit()` returns `abcdef012345`.

### 5. Malformed pseudo-versions rejected

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/bad_pseudo`
2. **Expected:** Passes. Strings like `v0.0.0-2026-abc` return errors rather than being accepted as generic semver prereleases.

### 6. ToolFQN round-trip

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseToolFQN`
2. **Expected:** Passes. `github.com/owner/repo@v1.0.0/my.tool` parses into Module+Version+Tool and `String()` reproduces the original.

### 7. PackageVer round-trip

1. Run `go test ./tool/... -v -count=1 -run TestFQNParsePackageVer`
2. **Expected:** Passes. `github.com/owner/repo@v1.0.0` parses and round-trips.

## Edge Cases

### Empty and malformed inputs

1. Run `go test ./tool/... -v -count=1 -run TestFQN` (full suite covers empty strings, missing @, missing /, trailing dots)
2. **Expected:** All return descriptive errors, none panic.

## Failure Signals

- Any subtest in `go test ./tool/... -run TestFQN` failing
- `go vet ./tool/...` reporting issues

## Not Proven By This UAT

- How downstream slices (S04-S12) consume these types at integration boundaries
- Runtime behavior — these are pure types with no I/O

## Notes for Tester

Tests run in ~3ms total. No external dependencies required.
 |
| S04 | Local cache layout | low | S03 | ✅ | # S04: Local cache layout — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T16:25:10.315Z

# S04: Local cache layout — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: Pure filesystem cache with no runtime behavior — validated entirely by unit tests.

## Preconditions

- Working directory: the M001-zku9aj worktree root
- Go toolchain available

## Smoke Test

Run `go test ./registry/... -v -count=1 -run TestCache` — all 6 subtests pass.

## Test Cases

### 1. Put + Has round-trip

1. Run `go test ./registry/... -v -count=1 -run TestCache/PutHasRoundTrip`
2. **Expected:** Passes. After Put, Has returns true for the same module+version.

### 2. Has returns false for missing entry

1. Run `go test ./registry/... -v -count=1 -run TestCache/HasMissingEntry`
2. **Expected:** Passes. Has returns false for a never-stored module+version.

### 3. Put + LoadArchive round-trip with real fixture

1. Run `go test ./registry/... -v -count=1 -run TestCache/PutLoadArchiveRoundTrip`
2. **Expected:** Passes. Archive written via Put loads successfully through packaging.LoadArchive with sha256 verification.

### 4. Path structure verification

1. Run `go test ./registry/... -v -count=1 -run TestCache/PathStructureVerification`
2. **Expected:** Passes. Files land at `<root>/<module>/@v/<version>.pkg`, `.manifest`, and `.info`.

### 5. TOOLBOX_CACHE_DIR override

1. Run `go test ./registry/... -v -count=1 -run TestCache/ToolboxCacheDirOverride`
2. **Expected:** Passes. Setting TOOLBOX_CACHE_DIR changes the root directory used by NewCache("").

### 6. LoadArchive on missing entry returns error

1. Run `go test ./registry/... -v -count=1 -run TestCache/LoadArchiveMissingEntryReturnsError`
2. **Expected:** Passes. LoadArchive returns a non-nil error for a missing module+version.

## Edge Cases

### Empty cache directory

1. Create a new Cache with a fresh temp dir, call Has — returns false, call LoadArchive — returns error.
2. **Expected:** No panics, clean error messages.

## Failure Signals

- Any subtest in `go test ./registry/... -run TestCache` failing
- `go vet ./registry/...` reporting issues

## Not Proven By This UAT

- Cache eviction or size management (not implemented)
- Concurrent write safety beyond OS-level guarantees
- Integration with downstream resolvers (S05, S06)

## Notes for Tester

Tests run in ~6ms total. No external dependencies required.
 |
| S05 | GitHub Releases source + PackageSource interface | high | S01, S02, S03, S04 | ✅ | # S05: GitHub Releases source + PackageSource interface — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T21:42:03.232Z

# S05: GitHub Releases source + PackageSource interface — UAT

**Milestone:** M001-zku9aj

## UAT Type
- UAT mode: artifact-driven
- Why: Library code with no runtime behavior — validated entirely by integration tests against emulate.

## Preconditions
- Node.js and npx available on PATH (for emulate)
- Working directory: the M001-zku9aj worktree root

## Smoke Test
Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource -timeout 60s` — all 5 subtests pass.

## Test Cases

### 1. Happy path — fetch archive + manifest from seeded release
1. Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource/happy_path -timeout 60s`
2. **Expected:** Passes. Fetch returns non-nil archive and manifest bytes. Log may note emulate JSON limitation.

### 2. Release not found — 404 for unknown version
1. Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource/release_not_found -timeout 60s`
2. **Expected:** Passes. Error wraps `ErrReleaseNotFound`.

### 3. Missing archive asset
1. Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource/missing_archive_asset -timeout 60s`
2. **Expected:** Passes. Error mentions missing archive.

### 4. Missing manifest asset
1. Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource/missing_manifest_asset -timeout 60s`
2. **Expected:** Passes. Error mentions missing manifest.

### 5. Empty release — no assets
1. Run `go test ./registry -v -count=1 -run TestGitHubReleaseSource/empty_release -timeout 60s`
2. **Expected:** Passes. Error mentions no assets.

## Edge Cases

### npx not available
1. Remove npx from PATH, run the tests.
2. **Expected:** Tests skip with "emulatetest: npx not available".

## Not Proven By This UAT
- Auth token handling for private repos (R003 partial — deferred to S08/S12)
- Integration with resolver orchestration (S08)
- Cache population after fetch (S08)
 |
| S06 | Git-source fallback resolver | high | S01, S02, S03, S04 | ✅ | TBD |
| S07 | Pseudo-version resolution | medium | S03, S06 | ⬜ | TBD |
| S08 | Resolver orchestration + Builder.AddFromRegistry | medium | S05, S06 | ⬜ | TBD |
| S09 | Toolset file parsing | medium | S08 | ⬜ | TBD |
| S10 | Lockfile generation and verification | medium | S09 | ⬜ | TBD |
| S11 | Replace directives | low | S09 | ⬜ | TBD |
| S12 | CLI commands + end-to-end UAT | low | S10, S11 | ⬜ | TBD |
