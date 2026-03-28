# S06: Git-source fallback resolver

**Goal:** GitSourceFallback implements PackageSource by cloning a git repo at a tagged version, running packaging.Pack, and returning archive+manifest bytes.
**Demo:** After this: TBD

## Tasks
- [x] **T01: Added GitSourceFallback to clone tagged repos, package them, and return archive plus manifest bytes with a passing happy-path test.** — Create registry/git_source.go with GitSourceFallback struct implementing PackageSource.Fetch. The Fetch method: (1) derives clone URL as "file://" + module path for tests or "https://" + module path for production, (2) shallow-clones at the version tag using exec.Command("git", "clone", "--depth=1", "--branch", tag, url, tmpDir), (3) calls packaging.Pack(tmpDir, outDir), (4) reads archive and manifest bytes from PackResult paths, (5) cleans up temp dirs. Add compile-time interface check. Then create registry/git_source_test.go with TestGitSource/happy_path using gitfixture.CreateTaggedRepoFromDir with the calc fixture at testutil/fixtures/toolbox.pkgs/calc, converting the repo path to a file:// URL. Verify Fetch returns non-empty archive and manifest bytes.
  - Estimate: 45m
  - Files: registry/git_source.go, registry/git_source_test.go
  - Verify: env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s
- [x] **T02: Added GitSourceFallback error-path tests for missing tags, missing package manifests, and corrupt package manifests.** — Extend registry/git_source_test.go with three error-path subtests under TestGitSource: (1) missing_tag — use gitfixture.CreateRepoMissingTag to create a repo with tag v1.0.0, then Fetch with version v9.9.9 (absent tag) and assert error is non-nil, (2) broken_repo — use gitfixture.CreateBrokenRepo to create a repo with no toolbox.devpkg.json, Fetch and assert Pack-related error, (3) corrupt_package — use gitfixture.CreateCorruptPackageRepo to create a repo with invalid JSON in toolbox.devpkg.json, Fetch and assert error. For all tests, construct GitSourceFallback with a URLPrefix of "file://" and the repo path. Run full regression suite to confirm no breakage.
  - Estimate: 30m
  - Files: registry/git_source_test.go
  - Verify: env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource -timeout 30s && env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry/... -v -count=1 -timeout 60s
