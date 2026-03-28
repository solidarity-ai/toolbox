---
estimated_steps: 19
estimated_files: 1
skills_used: []
---

# T02: Integration tests for GitHubReleaseSource against emulate

Write integration tests in `registry/source_test.go` that exercise `GitHubReleaseSource.Fetch()` against a real emulate subprocess using the seed helpers from S01/S02.

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

## Inputs

- ``registry/source.go` — PackageSource interface and GitHubReleaseSource to test`
- ``registry/testutil/emulatetest/emulatetest.go` — Start(t) for emulate server`
- ``registry/testutil/emulatetest/seed.go` — SeedPackageRelease, SeedMissingAssetRelease, SeedEmptyRelease helpers`
- ``registry/testutil/emulatetest/testdata/calc-pkg/` — fixture package directory`

## Expected Output

- ``registry/source_test.go` — Integration tests for GitHubReleaseSource covering happy path, 404, missing assets, empty release`

## Verification

go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s
