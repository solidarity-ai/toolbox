# S06: Git-source fallback resolver — Research

**Date:** 2026-03-27

## Summary

S06 implements a `GitSourceFallback` type that satisfies the `PackageSource` interface (defined in S05's `registry/source.go`). Given a module path and version, it derives a git clone URL, clones at the tag, runs `packaging.Pack` on the checkout, and returns archive+manifest bytes. This is straightforward — all building blocks exist: `PackageSource` interface, `packaging.Pack`, `gitfixture` test helpers, and the S02 failure fixtures.

The only real work is: (1) a new `registry/git_source.go` file with ~80 lines of clone+pack logic, (2) a test file using gitfixture to create repos and verify Fetch works, and (3) error-path tests using S02's broken/missing-tag fixtures.

## Recommendation

Single-file implementation in `registry/git_source.go`. Use `exec.Command("git", "clone", "--depth=1", "--branch", tag, url, tmpDir)` for the clone — same pattern gitfixture already uses. After clone, call `packaging.Pack(tmpDir, outDir)` and read the resulting files back as bytes. Return them via the `PackageSource.Fetch` signature. Test against file:// URLs from gitfixture repos.

## Implementation Landscape

### Key Files

- `registry/source.go` — Contains `PackageSource` interface and `GitHubReleaseSource`. The new `GitSourceFallback` implements the same interface.
- `registry/git_source.go` — **New file.** `GitSourceFallback` struct with `Fetch(ctx, module, version) (archive, manifest, error)`. Derives clone URL from module path (`https://<modulepath>`), shallow-clones at tag, calls `packaging.Pack`, reads result bytes.
- `registry/git_source_test.go` — **New file.** Tests using `gitfixture.CreateTaggedRepoFromDir` with the calc fixture for happy path. Error tests: `CreateBrokenRepo` (no manifest → Pack fails), `CreateRepoMissingTag` (clone at absent tag fails).
- `packaging/packaging.go` — `Pack(dir, outDir) (PackResult, error)` — called by git source after clone. `PackResult` has `ArchivePath` and `ManifestPath`.
- `registry/testutil/gitfixture/gitfixture.go` — Provides `CreateTaggedRepo`, `CreateTaggedRepoFromDir`, `CreateBrokenRepo`, `CreateRepoMissingTag`, `CreateCorruptPackageRepo`.
- `testutil/fixtures/toolbox.pkgs/` — Contains the calc fixture package used by existing tests.

### Build Order

1. **T01: Implement `GitSourceFallback`** — Create `registry/git_source.go` with the struct, constructor, and `Fetch` method. Core logic: derive clone URL, shallow clone at tag to temp dir, `packaging.Pack`, read bytes, clean up. Include `var _ PackageSource = (*GitSourceFallback)(nil)` compile check.
2. **T02: Happy-path + error-path tests** — Create `registry/git_source_test.go`. Happy path uses `gitfixture.CreateTaggedRepoFromDir` with the calc fixture, converting the repo path to a `file://` URL for cloning. Error paths: missing tag (clone fails), broken repo (Pack fails on missing manifest), corrupt package (Pack fails on invalid JSON).

### Verification Approach

- `go test ./registry -v -count=1 -run TestGitSource -timeout 30s` — all subtests pass
- `go vet ./registry/...` — clean
- Full suite regression: `go test ./registry/... -v -count=1 -timeout 60s` — no regressions

## Constraints

- Must use real `git` CLI via `exec.Command` (established pattern from gitfixture, per D001 philosophy of testing real behavior)
- Must produce the same `([]byte, []byte, error)` return as `PackageSource.Fetch` — archive bytes and manifest bytes
- Clone URL derivation: `https://` + module path for real usage; `file://` + repo path for tests
- `packaging.Pack` requires the source dir to contain a valid `toolbox.devpkg.json` — this is what makes broken-repo tests fail correctly

## Common Pitfalls

- **Worktree path resolution in tests** — Per KNOWLEDGE.md, `go test` may resolve through the linked checkout. Use `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ...` if needed.
- **Git clone URL for file:// paths** — Must use `file://` prefix for local repos to avoid git treating paths as relative. gitfixture repos return absolute paths, so `"file://" + repoPath` works.
- **Temp dir cleanup** — Clone into `t.TempDir()` in tests (auto-cleanup). In production code, use `os.MkdirTemp` + defer `os.RemoveAll`.
