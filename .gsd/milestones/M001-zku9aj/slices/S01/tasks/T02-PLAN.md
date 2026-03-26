---
estimated_steps: 30
estimated_files: 2
skills_used: []
---

# T02: Build local git fixture builder with tagged-repo test

## Description

Build the local git fixture builder that creates temporary bare-compatible git repos with tagged commits containing real package files. This is the test infrastructure for S06 (git-source fallback) — it produces repos that the git-source resolver will clone from using `file://` URLs.

**Slice context:** S01 builds two independent test subsystems. T01 built the emulate subsystem. This task builds the git fixture subsystem. They share no code.

**Key facts the executor must know:**
- Use `exec.Command("git", ...)` for all git operations — no Go git libraries
- Must set git user config explicitly for CI: use `-c user.name=test -c user.email=test@test.com` on commit commands
- Create non-bare repos (simpler: init, write files, add, commit, tag). The git-source resolver in S06 will clone from the directory path.
- Use `t.TempDir()` for automatic cleanup
- The test should clone the created repo to a separate temp dir and verify the tag and files
- Use `testutil/fixtures/toolbox.pkgs/calc/` as a real package source directory for one test
- No new Go dependencies — `os/exec` and `os` only

## Steps

1. **Create `registry/testutil/gitfixture/gitfixture.go`**:
   - `CreateTaggedRepo(t *testing.T, tag string, files map[string][]byte) string` — creates temp dir, `git init`, writes files at given paths, `git add .`, `git -c user.name=test -c user.email=test@test.com commit -m "initial"`, `git tag <tag>`, returns absolute path to the repo directory
   - `CreateTaggedRepoFromDir(t *testing.T, tag string, srcDir string) string` — copies all files from srcDir into temp repo, commits and tags. Uses `filepath.WalkDir` to copy. Returns absolute path.
   - Helper: `runGit(t *testing.T, dir string, args ...string) string` — runs `git` with args in the given dir, returns stdout, fails test on non-zero exit. Captures stderr for diagnostics.
   - All temp dirs created via `t.TempDir()` for automatic cleanup

2. **Create `registry/testutil/gitfixture/gitfixture_test.go`**:
   - `TestCreateTaggedRepo`: Create repo with tag "v1.0.0" and files `{"hello.txt": []byte("hello")}`. Clone to separate temp dir. Verify: `git tag -l` includes "v1.0.0", `git checkout v1.0.0` succeeds, `hello.txt` contains "hello".
   - `TestCreateTaggedRepoFromDir`: Create repo from `testutil/fixtures/toolbox.pkgs/calc/` with tag "v2.0.0". Clone to separate temp dir. Checkout tag. Verify `toolbox.devpkg.json` exists and contains `"name": "calc"`.
   - `TestMultipleTags`: Create repo, add first tag, modify a file, add second tag. Verify both tags exist and files differ between them.

## Must-Haves

- [ ] `CreateTaggedRepo` creates a git repo with specified files at a given tag
- [ ] `CreateTaggedRepoFromDir` copies files from an existing directory into a tagged repo
- [ ] Git user config is set explicitly (works in CI without global git config)
- [ ] Tests verify repos are cloneable and tags contain expected files
- [ ] All temp dirs use `t.TempDir()` for automatic cleanup

## Verification

- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s` passes with all tests green
- Test output shows repos created, cloned, tags verified, files checked

## Inputs

- ``testutil/fixtures/toolbox.pkgs/calc/` — source package directory for CreateTaggedRepoFromDir test`
- ``testutil/fixtures/toolbox.pkgs/calc/toolbox.devpkg.json` — manifest file verified in test assertions`

## Expected Output

- ``registry/testutil/gitfixture/gitfixture.go` — CreateTaggedRepo, CreateTaggedRepoFromDir, runGit helper`
- ``registry/testutil/gitfixture/gitfixture_test.go` — tests proving tagged repos are cloneable with correct files`

## Verification

cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s
