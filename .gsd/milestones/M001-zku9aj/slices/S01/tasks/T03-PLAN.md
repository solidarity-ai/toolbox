---
estimated_steps: 32
estimated_files: 1
skills_used: []
---

# T03: Add setup-node to CI workflow and verify full registry test suite

## Description

Update the GitHub Actions CI workflow to install Node.js and pre-install the `emulate` npm package, then verify the full registry test suite passes locally.

**Slice context:** T01 built the emulate subsystem and T02 built the git fixture subsystem. This task ensures both work in CI and verifies them together.

**Key facts the executor must know:**
- The CI file is `.github/workflows/ci.yml`
- Currently it has: checkout → setup-go → rust-toolchain → cache rust → build rust → rust tests → go fmt → go vet → go test
- Node.js is needed because emulate is an npm package started via `npx emulate start`
- Pre-installing emulate avoids the `npx` download delay during tests: `npm install -g emulate`
- The `emulate` package is at version 0.3.0 on npm
- Add `actions/setup-node@v4` with `node-version: '22'` BEFORE the Go test step
- The emulate tests use `t.Skip()` when npx is not available, so existing CI without node would just skip — but we want them to actually run

## Steps

1. **Edit `.github/workflows/ci.yml`**:
   - Add `actions/setup-node@v4` step with `node-version: '22'` after the checkout step (before Go/Rust setup is fine, order among setup steps doesn't matter)
   - Add a step: `name: Install emulate` with `run: npm install -g emulate` to pre-install so npx doesn't download on first use
   - Keep all existing steps unchanged

2. **Run full registry test suite locally**:
   - `go test ./registry/... -v -count=1 -timeout 60s` — verify all emulate and git fixture tests pass together
   - Verify no port conflicts or race conditions between test packages

3. **Validate CI config**:
   - Ensure YAML is valid (no syntax errors)
   - Verify the step ordering makes sense

## Must-Haves

- [ ] CI workflow includes `setup-node` with Node.js 22
- [ ] CI workflow pre-installs `emulate` npm package
- [ ] `go test ./registry/... -v -count=1 -timeout 60s` passes locally
- [ ] All existing CI steps remain unchanged

## Verification

- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo 'YAML valid'` — CI config is valid YAML
- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'setup-node' .github/workflows/ci.yml && echo 'setup-node present'`
- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'emulate' .github/workflows/ci.yml && echo 'emulate install present'`
- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/... -v -count=1 -timeout 60s` passes

## Inputs

- ``.github/workflows/ci.yml` — existing CI configuration to modify`
- ``registry/testutil/emulatetest/emulatetest.go` — emulate lifecycle manager (from T01)`
- ``registry/testutil/emulatetest/emulatetest_test.go` — emulate tests (from T01)`
- ``registry/testutil/gitfixture/gitfixture.go` — git fixture builder (from T02)`
- ``registry/testutil/gitfixture/gitfixture_test.go` — git fixture tests (from T02)`

## Expected Output

- ``.github/workflows/ci.yml` — updated with setup-node and emulate install steps`

## Verification

cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'setup-node' .github/workflows/ci.yml && grep -q 'emulate' .github/workflows/ci.yml && go test ./registry/... -v -count=1 -timeout 60s
