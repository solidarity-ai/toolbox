# S05: GitHub Releases source + PackageSource interface — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-28T00:36:53.060Z

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

