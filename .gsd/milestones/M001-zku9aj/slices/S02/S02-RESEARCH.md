# S02: Failure scenario test builders — Research

**Date:** 2026-03-27

## Summary

S02 adds failure scenario builders to the existing `emulatetest` and `gitfixture` test infrastructure. The downstream resolver slices (S05 GitHub Releases source, S06 git-source fallback) need to test error paths: corrupt archives, mismatched sha256 hashes, missing release assets, and broken git repos. The S01 happy-path builders (`SeedPackageRelease`, `CreateTaggedRepo`) are solid — S02 extends them with targeted corruption/omission helpers.

This is straightforward work. The patterns are established, the code is clean, and the failure modes are well-defined by `packaging/internal/archive/archive.go`'s `LoadArchive` validation chain: sha256 mismatch, missing sha256, internal/external manifest mismatch, missing internal manifest.

## Recommendation

Add failure scenario seed helpers to `emulatetest/seed.go` and `gitfixture/gitfixture.go`. Each helper produces a specific broken artifact that downstream tests can use. Keep them as composable building blocks — not test cases themselves. Test each builder with a small integration test confirming the artifact is actually broken in the expected way.

Specific builders needed:

**emulatetest (GitHub API failures):**
1. `SeedCorruptArchiveRelease` — uploads a valid manifest but corrupt archive bytes (sha256 won't match)
2. `SeedMismatchedHashRelease` — uploads valid archive but manifest with wrong sha256
3. `SeedMissingAssetRelease` — creates release with only manifest (no archive) or only archive (no manifest)
4. `SeedEmptyRelease` — creates release with no assets at all

**gitfixture (git failures):**
1. `CreateBrokenRepo` — repo with missing expected files (e.g. no `toolbox.devpkg.json`)
2. `CreateRepoMissingTag` — repo exists but requested tag doesn't
3. `CreateCorruptPackageRepo` — repo with invalid `toolbox.devpkg.json` (bad JSON)

## Implementation Landscape

### Key Files

- `registry/testutil/emulatetest/seed.go` — Add failure seed helpers alongside existing `SeedPackageRelease`. Reuse `CreateRepo`, `CreateRelease`, `UploadReleaseAsset` internals.
- `registry/testutil/emulatetest/emulatetest_test.go` — Add tests verifying each failure builder produces the expected broken state.
- `registry/testutil/gitfixture/gitfixture.go` — Add broken-repo creation helpers alongside `CreateTaggedRepo`.
- `registry/testutil/gitfixture/gitfixture_test.go` — Add tests verifying broken fixtures.
- `packaging/internal/archive/archive.go` — Reference only (read). `LoadArchive` defines the validation chain these builders must break: sha256 check (line 130), missing sha256 (line 128), internal/external manifest mismatch (line 151), missing internal manifest (line 452).

### Build Order

1. **Emulate failure builders first** — they're consumed by S05 (highest-risk downstream slice). Add `SeedCorruptArchiveRelease`, `SeedMismatchedHashRelease`, `SeedMissingAssetRelease`, `SeedEmptyRelease` to `seed.go` with tests.
2. **Git fixture failure builders second** — consumed by S06. Add `CreateBrokenRepo`, `CreateRepoMissingTag`, `CreateCorruptPackageRepo` to `gitfixture.go` with tests.
3. **Verify full test suite** — `go test ./registry/... -v -count=1` to confirm no regressions.

### Verification Approach

- `go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` — all tests pass including new failure scenario tests
- `go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 60s` — all tests pass including broken repo tests  
- `go test ./registry/... -v -count=1 -timeout 60s` — full suite passes, no port conflicts
- Each failure builder test should verify the artifact is broken in the expected way (e.g. `LoadArchive` returns an error containing "sha256 mismatch")

## Constraints

- Failure builders must use the same `SeedClient` / `*Server` pattern — no new test infrastructure needed.
- `SeedCorruptArchiveRelease` and `SeedMismatchedHashRelease` should return `*SeedResult` with the corrupt bytes so S05 can use the workaround for emulate's broken binary download.
- Git fixture helpers must use explicit `-c user.name=test` for CI compatibility (pattern from S01).

## Common Pitfalls

- **Corrupt archive vs mismatched hash** — these are different scenarios. Corrupt archive = random bytes where archive should be (LoadArchive can't decompress). Mismatched hash = valid archive but manifest sha256 doesn't match (LoadArchive decompresses fine but hash check fails). Both must be testable independently.
- **emulate asset download limitation** — emulate v0.3.0 returns JSON instead of binary for asset downloads. The `SeedResult.ArchiveBytes` / `ManifestBytes` workaround must apply to failure builders too, since S05 will need the corrupt bytes directly.
