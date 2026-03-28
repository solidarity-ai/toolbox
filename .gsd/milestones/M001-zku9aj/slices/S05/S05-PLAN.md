# S05: GitHub Releases source + PackageSource interface

**Goal:** Define the PackageSource interface and implement GitHubReleaseSource — given (module, version), fetch .toolbox.pkg and toolbox.pkg.json from GitHub Release assets with GITHUB_BASE_URL override for emulate testing.
**Demo:** After this: # S05: GitHub Releases source + PackageSource interface — UAT

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


## Tasks
- [x] **T01: Added the PackageSource interface and a GitHubReleaseSource with emulate-backed integration tests for success and release/asset failure modes.** — Define the `PackageSource` interface with a single `Fetch` method and implement `GitHubReleaseSource` that resolves packages from GitHub Release assets.

The interface: `Fetch(ctx context.Context, module ModulePath, version Version) (archive []byte, manifest []byte, err error)`

`GitHubReleaseSource` struct holds `baseURL` (defaults to `https://api.github.com`) and `httpClient`. It:
1. Derives owner/repo from module path segments (e.g. `github.com/owner/repo` → owner=segments[1], repo=segments[2])
2. Calls `GET /repos/{owner}/{repo}/releases/tags/v{version}` (prepend 'v' only if version doesn't start with 'v' — but Version type already includes the 'v' prefix per S03)
3. Finds assets by name: one ending in `.toolbox.pkg` (archive) and one named `toolbox.pkg.json` (manifest)
4. Downloads each asset via `GET /repos/{owner}/{repo}/releases/assets/{id}` with `Accept: application/octet-stream`
5. Returns (archiveBytes, manifestBytes, nil) on success

Error cases to handle:
- Release not found (404) → return a sentinel or typed error
- Archive asset not found in release → descriptive error
- Manifest asset not found in release → descriptive error
- No assets at all → descriptive error
- HTTP errors during download → wrap with context

Constructor: `NewGitHubReleaseSource(baseURL string, client *http.Client) *GitHubReleaseSource`. Empty baseURL defaults to `https://api.github.com`.
  - Estimate: 45m
  - Files: registry/source.go
  - Verify: go build ./registry/... && go vet ./registry/...
- [x] **T02: Verified the existing emulate-backed GitHubReleaseSource integration suite and documented the worktree-safe Go test invocation needed to run it reliably here.** — Write integration tests in `registry/source_test.go` that exercise `GitHubReleaseSource.Fetch()` against a real emulate subprocess using the seed helpers from S01/S02.

Test cases (all use `emulatetest.Start(t)` for the emulate server):

1. **Happy path**: Call `SeedPackageRelease` to create a release with real archive+manifest assets. Call `Fetch()` with the seeded module+version. Due to the emulate binary download bug (returns JSON instead of binary for asset downloads), the test should verify that the Fetch call completes without error and returns non-nil bytes. If the returned bytes match `SeedResult.ArchiveBytes`/`ManifestBytes`, assert that. If emulate returns JSON instead, document the limitation and verify the API call sequence works (no HTTP errors, correct asset identification).

2. **Release not found**: Call `Fetch()` with a version that was never seeded. Expect an error (ideally a typed/sentinel error indicating 'not found').

3. **Missing archive asset**: Use `SeedMissingAssetRelease(... , "archive")` which uploads only the manifest. Call `Fetch()`. Expect an error about missing archive.

4. **Missing manifest asset**: Use `SeedMissingAssetRelease(... , "manifest")` which uploads only the archive. Call `Fetch()`. Expect an error about missing manifest.

5. **Empty release**: Use `SeedEmptyRelease()`. Call `Fetch()`. Expect an error about no assets.

Test setup pattern:
```go
func TestGitHubReleaseSource(t *testing.T) {
    srv := emulatetest.Start(t)
    src := NewGitHubReleaseSource(srv.BaseURL(), srv.Client())
    // subtests...
}
```

Module path for tests: use `ParseModulePath` to create a valid module path like `github.com/admin/testrepo` (emulate creates repos under the 'admin' owner per D006).

Version: use `ParseVersion("v1.0.0")` or similar.

Fixture directory for SeedPackageRelease: use `registry/testutil/emulatetest/testdata/calc-pkg` (the existing fixture from S01).

Important: each subtest should use a unique repo name to avoid collisions since the emulate server is shared across the test binary.
  - Estimate: 45m
  - Files: registry/source_test.go
  - Verify: go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s
