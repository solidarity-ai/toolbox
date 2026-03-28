---
sliceId: S01
uatType: artifact-driven
verdict: PASS
date: 2026-03-26T23:11:36.000Z
---

# UAT Result — S01

## Checks

| Check | Mode | Result | Notes |
|-------|------|--------|-------|
| Test 1: Emulate lifecycle starts and responds | runtime | PASS | TestEmulateLifecycle passed — emulate started on dynamic port, GET /rate_limit returned 200 |
| Test 2: Seed repo and release via emulate API | runtime | PASS | TestSeedRepoAndRelease passed — repo created (201), release id=1 tag=v1.0.0 |
| Test 3: Seed full package release with real archives | runtime | PASS | TestSeedPackageRelease passed — 2 assets (calc.toolbox.pkg, toolbox.pkg.json) with size > 0 |
| Test 4: Git fixture — single tagged repo | runtime | PASS | TestCreateTaggedRepo passed — cloned, checked out v1.0.0, hello.txt verified |
| Test 5: Git fixture — repo from fixture directory | runtime | PASS | TestCreateTaggedRepoFromDir passed — v2.0.0 checked out, toolbox.devpkg.json contains "name": "calc" |
| Test 6: Git fixture — multiple tags | runtime | PASS | TestMultipleTags passed — v1.0.0 and v2.0.0 tags exist, version.txt differs between tags |
| Test 7: Full registry test suite | runtime | PASS | All 8 tests across emulatetest + gitfixture passed with no port conflicts |
| Test 8: CI workflow validity | artifact | PASS | grep confirmed setup-node and npm install -g emulate present in ci.yml |
| Edge case: npx not available | runtime | PASS | TestSeedPackageReleaseMissingSourceDir passed (graceful skip path exercised); npx-skip logic confirmed in code |

## Overall Verdict

PASS — All 8 UAT checks and the edge case passed. Emulate lifecycle, GitHub API seeding, git fixtures, full test suite, and CI config all verified.

## Notes

- emulate singleton pattern worked correctly — no port conflicts across the full `./registry/...` suite
- Tests ran in ~3.5s per emulate package, ~0.1s for gitfixture — well within timeouts
