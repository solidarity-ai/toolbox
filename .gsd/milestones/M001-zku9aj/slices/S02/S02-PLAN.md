# S02: Failure scenario test builders

**Goal:** Add failure scenario seed helpers to emulatetest and gitfixture packages so downstream resolver slices (S05, S06) can test error paths: corrupt archives, mismatched hashes, missing assets, empty releases, broken git repos.
**Demo:** After this: TBD

## Tasks
- [x] **T01: Added emulate release seeding helpers for corrupt, mismatched, missing-asset, and empty-release failure scenarios with tests.** — Add four failure scenario helpers to emulatetest/seed.go:

1. `SeedCorruptArchiveRelease(owner, repo, tag, pkgDir)` — packs a real package, uploads valid manifest but replaces archive bytes with random garbage. Returns SeedResult with corrupt ArchiveBytes.
2. `SeedMismatchedHashRelease(owner, repo, tag, pkgDir)` — packs a real package, uploads valid archive but modifies the manifest sha256 to a wrong value before uploading. Returns SeedResult with real ArchiveBytes and tampered ManifestBytes.
3. `SeedMissingAssetRelease(owner, repo, tag, pkgDir, mode)` — creates release with only one asset. `mode` selects which is missing: "archive" (manifest only) or "manifest" (archive only).
4. `SeedEmptyRelease(owner, repo, tag)` — creates repo + release with zero assets.

All helpers follow the existing SeedPackageRelease pattern: create repo (ignore already-exists), create release, upload assets selectively. Return SeedResult so S05 can access bytes directly (emulate binary download workaround).

Add tests in emulatetest_test.go verifying:
- Corrupt archive: SeedResult.ArchiveBytes is not valid (attempt LoadArchive fails)
- Mismatched hash: manifest sha256 does not match actual archive sha256
- Missing asset: release has exactly 1 asset with expected name
- Empty release: release has 0 assets
  - Estimate: 45m
  - Files: registry/testutil/emulatetest/seed.go, registry/testutil/emulatetest/emulatetest_test.go
  - Verify: go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s
- [x] **T02: Added git fixture failure builders and tests for missing manifest, missing tag, and corrupt package scenarios.** — Add three failure scenario helpers to gitfixture/gitfixture.go:

1. `CreateBrokenRepo(t, tag)` — creates a tagged repo with no `toolbox.devpkg.json` (just a README). S06 resolver expects this file; its absence is the failure.
2. `CreateRepoMissingTag(t, existingTag, missingTag)` — creates repo tagged at existingTag. Returns repo path. Caller can attempt checkout of missingTag which won't exist.
3. `CreateCorruptPackageRepo(t, tag)` — creates tagged repo where `toolbox.devpkg.json` contains invalid JSON (`{broken`).

All helpers use existing runGit/commitAllAndTag internals. Follow the established pattern: t.Helper(), t.TempDir(), explicit git user config.

Add tests in gitfixture_test.go verifying:
- Broken repo: clone + ls shows no toolbox.devpkg.json
- Missing tag: clone succeeds, `git tag -l missingTag` returns empty
- Corrupt package: toolbox.devpkg.json exists but json.Unmarshal fails

Finally run `go test ./registry/... -v -count=1 -timeout 60s` to confirm no regressions across both packages.
  - Estimate: 30m
  - Files: registry/testutil/gitfixture/gitfixture.go, registry/testutil/gitfixture/gitfixture_test.go
  - Verify: go test ./registry/... -v -count=1 -timeout 60s
