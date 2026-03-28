---
sliceId: S02
uatType: artifact-driven
verdict: PASS
date: 2026-03-27T14:40:00.000Z
---

# UAT Result — S02

## Checks

| Check | Mode | Result | Notes |
|-------|------|--------|-------|
| Corrupt archive release | artifact | PASS | TestSeedCorruptArchiveRelease passed — archive bytes fail LoadArchive with sha256 mismatch |
| Mismatched hash release | artifact | PASS | TestSeedMismatchedHashRelease passed — tampered manifest hash differs from actual archive hash |
| Missing asset release (archive mode) | artifact | PASS | TestSeedMissingAssetRelease/missing_archive passed — release has 1 asset (manifest only) |
| Missing asset release (manifest mode) | artifact | PASS | TestSeedMissingAssetRelease/missing_manifest passed — release has 1 asset (archive only) |
| Empty release | artifact | PASS | TestSeedEmptyRelease passed — release has 0 assets |
| Broken git repo | artifact | PASS | TestCreateBrokenRepo passed — cloned repo has no toolbox.devpkg.json |
| Missing git tag | artifact | PASS | TestCreateRepoMissingTag passed — repo has existing tag but missing tag returns empty |
| Corrupt package repo | artifact | PASS | TestCreateCorruptPackageRepo passed — toolbox.devpkg.json exists but json.Unmarshal fails |
| Full regression suite | artifact | PASS | `go test ./registry/... -count=1 -timeout 60s` — all packages pass, no regressions |
| CI green on feature branch | runtime | PASS | GH Actions run 23651579812 on milestone/M001-zku9aj — all steps passed in 3m30s (Go fmt, vet, test, Rust tests) |

## Overall Verdict

PASS — All 7 failure-scenario builders pass locally and in CI. Full registry regression suite green. CI run 23651579812 confirms no regressions on the feature branch.

## Notes

- Emulate tests took ~3.5s locally (includes server startup). Gitfixture tests ~0.2s.
- CI run includes Rust tests, Go fmt/vet, and full Go test suite — all passed.
- npx-skip edge case not tested (npx available in both local and CI environments).
- CI warning about Node.js 20 deprecation in GH Actions — not related to this slice.
