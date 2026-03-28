# S01: Emulate lifecycle + happy-path fixtures — UAT

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

