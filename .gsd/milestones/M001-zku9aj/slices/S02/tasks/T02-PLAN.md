---
estimated_steps: 10
estimated_files: 2
skills_used: []
---

# T02: Add git fixture failure builders and tests, verify full suite

Add three failure scenario helpers to gitfixture/gitfixture.go:

1. `CreateBrokenRepo(t, tag)` — creates a tagged repo with no `toolbox.devpkg.json` (just a README). S06 resolver expects this file; its absence is the failure.
2. `CreateRepoMissingTag(t, existingTag, missingTag)` — creates repo tagged at existingTag. Returns repo path. Caller can attempt checkout of missingTag which won't exist.
3. `CreateCorruptPackageRepo(t, tag)` — creates tagged repo where `toolbox.devpkg.json` contains invalid JSON (`{broken`).

All helpers use existing runGit/commitAllAndTag internals. Follow the established pattern: t.Helper(), t.TempDir(), explicit git user config.

Add tests in gitfixture_test.go verifying:
- Broken repo: clone + ls shows no toolbox.devpkg.json
- Missing tag: clone succeeds, `git tag -l missingTag` returns empty
- Corrupt package: toolbox.devpkg.json exists but json.Unmarshal fails

Finally run `go test ./registry/... -v -count=1 -timeout 60s` to confirm no regressions across both packages.

## Inputs

- ``registry/testutil/gitfixture/gitfixture.go` — existing CreateTaggedRepo, runGit, commitAllAndTag`
- ``registry/testutil/gitfixture/gitfixture_test.go` — existing test patterns`

## Expected Output

- ``registry/testutil/gitfixture/gitfixture.go` — three new exported Create* functions`
- ``registry/testutil/gitfixture/gitfixture_test.go` — three new test functions`

## Verification

go test ./registry/... -v -count=1 -timeout 60s
