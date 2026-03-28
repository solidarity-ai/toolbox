---
estimated_steps: 1
estimated_files: 1
skills_used: []
---

# T02: Add error-path tests for missing tag, broken repo, and corrupt package

Extend registry/git_source_test.go with three error-path subtests under TestGitSource: (1) missing_tag — use gitfixture.CreateRepoMissingTag to create a repo with tag v1.0.0, then Fetch with version v9.9.9 (absent tag) and assert error is non-nil, (2) broken_repo — use gitfixture.CreateBrokenRepo to create a repo with no toolbox.devpkg.json, Fetch and assert Pack-related error, (3) corrupt_package — use gitfixture.CreateCorruptPackageRepo to create a repo with invalid JSON in toolbox.devpkg.json, Fetch and assert error. For all tests, construct GitSourceFallback with a URLPrefix of "file://" and the repo path. Run full regression suite to confirm no breakage.

## Inputs

- ``registry/git_source.go` — GitSourceFallback implementation from T01`
- ``registry/git_source_test.go` — Test file with happy_path from T01`
- ``registry/testutil/gitfixture/gitfixture.go` — CreateBrokenRepo, CreateRepoMissingTag, CreateCorruptPackageRepo helpers`

## Expected Output

- ``registry/git_source_test.go` — Extended with missing_tag, broken_repo, corrupt_package subtests`

## Verification

env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource -timeout 30s && env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/... -v -count=1 -timeout 60s
