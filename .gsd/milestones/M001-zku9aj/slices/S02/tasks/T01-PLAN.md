---
estimated_steps: 11
estimated_files: 2
skills_used: []
---

# T01: Add emulate failure scenario seed helpers and tests

Add four failure scenario helpers to emulatetest/seed.go:

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

## Inputs

- ``registry/testutil/emulatetest/seed.go` — existing SeedClient, SeedPackageRelease, SeedResult pattern`
- ``registry/testutil/emulatetest/emulatetest.go` — Server.Seed(), Start(t) lifecycle`
- ``registry/testutil/emulatetest/emulatetest_test.go` — existing test patterns`
- ``packaging/internal/archive/archive.go` — LoadArchive validation chain (reference only)`

## Expected Output

- ``registry/testutil/emulatetest/seed.go` — four new exported Seed* functions`
- ``registry/testutil/emulatetest/emulatetest_test.go` — four new test functions`

## Verification

go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s
