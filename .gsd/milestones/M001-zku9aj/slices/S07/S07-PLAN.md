# S07: Pseudo-version resolution

**Goal:** Extend GitSourceFallback so pseudo-version package pins resolve a specific untagged git commit, package that checkout locally, and preserve the existing tagged fast path.
**Demo:** After this: After this: TBD

## Tasks
- [x] **T01: Added a reusable pseudo-version git fixture helper and contract test that prove an untagged commit resolves to deterministic pseudo-version metadata.** — Build a reusable local git fixture for pseudo-version tests so registry tests can consume real commit metadata instead of reimplementing git plumbing.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| `git` CLI in fixture helper | Fail the helper test immediately with the git command, repo path, and stderr context | Not applicable in unit-test scope; command failure is surfaced the same way | Not applicable — git output is treated as command text and validated in tests |

## Load Profile

- **Shared resources**: temp directories, local git objects, test-process filesystem state
- **Per-operation cost**: repo init + tag + one extra commit + `rev-parse` / `show` calls
- **10x breakpoint**: process startup and temp-dir churn increase first, but still stay well within unit-test scale

## Negative Tests

- **Malformed inputs**: The contract test fails if the helper returns an empty pseudo version, timestamp, short SHA, or full SHA.
- **Error paths**: The contract test proves the pseudo commit is untagged so downstream resolver tests do not accidentally pass via tag lookup.
- **Boundary conditions**: The short SHA stays 12 characters and resolves to the returned full commit.

## Steps

1. Add an exported helper in `registry/testutil/gitfixture/gitfixture.go` that creates a repo from the calc fixture, tags the initial commit, writes a second untagged commit with an explicit author date, and returns pseudo-version metadata for that second commit.
2. Derive the pseudo-version timestamp from the commit author date in UTC and expose both short and full commit IDs to downstream tests.
3. Add a focused contract test in `registry/testutil/gitfixture/gitfixture_test.go` that clones the repo, resolves the short SHA, and proves the returned pseudo version points at the untagged commit.
4. Keep the helper compatible with the existing local `file://` clone flow used throughout `registry` tests.

## Must-Haves

- [ ] Returned metadata includes repo path, pseudo-version string, timestamp, 12-character commit prefix, and full commit SHA for the untagged commit.
- [ ] The pseudo-version commit is not tagged, so S07 tests exercise commit resolution rather than tag lookup.
- [ ] The helper remains reusable from `registry/git_source_test.go` without duplicating git setup.

## Done When

- The pseudo-version fixture helper and its focused contract test pass independently.
  - Estimate: 45m
  - Files: registry/testutil/gitfixture/gitfixture.go, registry/testutil/gitfixture/gitfixture_test.go, testutil/fixtures/toolbox.pkgs/calc/toolbox.devpkg.json
  - Verify: - `GOWORK=$(pwd)/go.work go test ./registry/testutil/gitfixture -v -count=1 -run TestCreatePseudoVersionRepo -timeout 30s`
- `GOWORK=$(pwd)/go.work go test ./registry/testutil/gitfixture -v -count=1 -timeout 30s`
- [ ] **T02: Teach GitSourceFallback to resolve pseudo-version commits** — Close the slice by extending the real git fetch path for pseudo versions while preserving the S06 tagged fast path.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| `git` CLI in `registry/git_source.go` | Return a phase-specific error that names the module, version, and failing git step | Surface the context cancellation / command failure from `exec.CommandContext` without retry loops | Treat ambiguous or unresolved commit prefixes as hard errors from `rev-parse` |
| `packaging.Pack` | Abort fetch and return a `pack cloned repo` error after checkout succeeds | Not applicable — packaging is local CPU/filesystem work | Treat missing or corrupt manifests as packaging errors after the pseudo-version checkout is proven |

## Load Profile

- **Shared resources**: temp clone directories, local git object database, packaging output directory
- **Per-operation cost**: tagged versions stay at one shallow clone; pseudo versions pay for one full clone plus `rev-parse`, `show`, and `checkout`
- **10x breakpoint**: pseudo-version full clones increase disk and process cost first, but only on pseudo-version requests

## Negative Tests

- **Malformed inputs**: Unknown or ambiguous 12-character commit prefixes must fail before packaging.
- **Error paths**: Timestamp mismatches between the pseudo version and git author time must return explicit errors.
- **Boundary conditions**: Existing tagged subtests still pass unchanged, proving the tagged fast path was preserved.

## Steps

1. Extend `registry/git_source_test.go` with pseudo-version happy-path, missing-commit, and timestamp-mismatch coverage using the new fixture helper, while keeping the existing tagged subtests intact.
2. Refactor `registry/git_source.go` so `Version.IsPseudo()` decides between the existing shallow tag checkout and a new pseudo-version checkout path.
3. In the pseudo-version path, clone without `--depth=1 --branch`, resolve `Version.PseudoCommit()` to a full commit with `git rev-parse --verify <prefix>^{commit}`, read the commit author timestamp in UTC, compare it to `Version.PseudoTimestamp()`, and check out the full commit.
4. Reuse the existing `packaging.Pack` + archive/manifest readback flow after checkout so the `PackageSource` contract stays unchanged.
5. Keep errors phase-specific enough that a future agent can distinguish clone, commit-resolution, timestamp, checkout, and packaging failures from test output alone.

## Must-Haves

- [ ] `registry` consumes `Version.IsPseudo()`, `PseudoTimestamp()`, and `PseudoCommit()` instead of re-parsing pseudo-version strings.
- [ ] Tagged versions keep the shallow clone fast path from S06.
- [ ] Pseudo versions resolve the requested commit, reject missing commit prefixes, and reject timestamp mismatches before packaging.
- [ ] `GitSourceFallback.Fetch` still returns archive + manifest bytes through the unchanged `PackageSource` interface.
- [ ] Existing `TestGitSource` tag-based subtests keep passing alongside the new pseudo-version coverage.

## Done When

- `TestGitSource` passes for both tagged and pseudo-version subtests, and the full `./registry/...` regression is green.
  - Estimate: 1h15m
  - Files: registry/git_source.go, registry/git_source_test.go, registry/source.go, tool/fqn.go, registry/testutil/gitfixture/gitfixture.go
  - Verify: - `GOWORK=$(pwd)/go.work go test ./registry -v -count=1 -run TestGitSource -timeout 30s`
- `GOWORK=$(pwd)/go.work go test ./registry/... -v -count=1 -timeout 60s`
