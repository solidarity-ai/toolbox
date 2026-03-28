---
estimated_steps: 1
estimated_files: 2
skills_used: []
---

# T01: Implement GitSourceFallback with happy-path test

Create registry/git_source.go with GitSourceFallback struct implementing PackageSource.Fetch. The Fetch method: (1) derives clone URL as "file://" + module path for tests or "https://" + module path for production, (2) shallow-clones at the version tag using exec.Command("git", "clone", "--depth=1", "--branch", tag, url, tmpDir), (3) calls packaging.Pack(tmpDir, outDir), (4) reads archive and manifest bytes from PackResult paths, (5) cleans up temp dirs. Add compile-time interface check. Then create registry/git_source_test.go with TestGitSource/happy_path using gitfixture.CreateTaggedRepoFromDir with the calc fixture at testutil/fixtures/toolbox.pkgs/calc, converting the repo path to a file:// URL. Verify Fetch returns non-empty archive and manifest bytes.

## Inputs

- ``registry/source.go` — PackageSource interface definition and ModulePath/Version type aliases`
- ``packaging/packaging.go` — Pack(dir, outDir) and PackResult with ArchivePath/ManifestPath`
- ``registry/testutil/gitfixture/gitfixture.go` — CreateTaggedRepoFromDir for test repo creation`
- ``testutil/fixtures/toolbox.pkgs/calc/` — calc fixture package with valid toolbox.devpkg.json`

## Expected Output

- ``registry/git_source.go` — GitSourceFallback struct with Fetch method implementing PackageSource`
- ``registry/git_source_test.go` — TestGitSource/happy_path subtest proving Fetch returns valid bytes`

## Verification

env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ./registry -v -count=1 -run TestGitSource/happy_path -timeout 30s
