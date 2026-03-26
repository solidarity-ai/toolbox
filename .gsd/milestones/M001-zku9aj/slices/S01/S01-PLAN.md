# S01: Emulate lifecycle + happy-path fixtures

**Goal:** Build shared Go test infrastructure — emulate lifecycle manager, GitHub API seed builder, and local git fixture builder — that all downstream registry slices (S02, S05, S06, S07, S08) depend on.
**Demo:** After this: TBD

## Tasks
- [x] **T01: Added shared emulate lifecycle and GitHub release seeding helpers with integration coverage for the real emulator API.** — ## Description

Build the emulate subprocess lifecycle manager and GitHub API seed builder that all downstream registry slices depend on. This is the highest-risk piece of S01 — managing a Node.js subprocess from Go tests with HTTP-based seeding.

**Slice context:** S01 builds shared test infrastructure for the registry milestone. This task creates the emulate subsystem. T02 creates the git fixture subsystem independently.

**Key facts the executor must know:**
- The npm package is `emulate` (NOT `@vercel-labs/emulate`). CLI: `npx emulate start --service github --port <PORT>`
- Auth works with `Authorization: token ghp_test123` — an `admin` user is auto-created
- Health check: `GET http://localhost:<PORT>/rate_limit` returns HTTP 200 when emulate is ready
- GitHub REST API works: POST `/user/repos`, POST `/repos/:owner/:repo/releases`, asset upload via `upload_url`
- **Known limitation:** Asset binary download does NOT work in emulate v0.3.0 (returns JSON instead of binary). This is S05's problem — S01 just uploads assets and verifies metadata. Document this in a code comment.
- No new Go dependencies. Use stdlib `net/http` and `os/exec`. Use `google/go-cmp` for test assertions.
- The `packaging.Pack(srcDir, outDir)` function produces real `.toolbox.pkg` + `toolbox.pkg.json` from a source package directory.
- Use `testutil/fixtures/toolbox.pkgs/calc/` as the source package for seeding.

## Failure Modes

| Dependency | On error | On timeout | On malformed response |
|------------|----------|-----------|----------------------|
| emulate subprocess | Log captured stderr, return descriptive error | 30s startup timeout, kill process, fail test with stderr dump | N/A — subprocess, not HTTP |
| emulate HTTP API | Return HTTP status + response body in error | Use http.Client timeout (10s default) | Return raw body in error for debugging |
| npx / Node.js | `t.Skip("npx not found")` to skip gracefully in environments without Node | N/A | N/A |
| packaging.Pack() | Propagate error — indicates broken fixture source | N/A | N/A |

## Negative Tests

- Verify that `SeedPackageRelease` with a nonexistent source dir returns a clear Pack error
- Verify that creating a release on a nonexistent repo returns a descriptive HTTP error

## Steps

1. **Create `registry/testutil/emulatetest/emulatetest.go`** — the lifecycle manager:
   - `Start(t *testing.T) *Server` — uses `sync.Once` to start emulate exactly once per test binary
   - Find a free port dynamically: `net.Listen("tcp", "127.0.0.1:0")`, get port, close listener
   - Start subprocess: `exec.Command("npx", "emulate", "start", "--service", "github", "--port", portStr)`
   - Redirect stderr to a `bytes.Buffer` for diagnostics; stdout can go to the buffer too
   - Poll `GET http://127.0.0.1:<port>/rate_limit` with 200ms intervals, 30s timeout
   - If `npx` is not found (exec.ErrNotFound), call `t.Skip("emulatetest: npx not available")`
   - `*Server` exposes `BaseURL() string`, `Client() *http.Client` (with auth header via RoundTripper), `Port() int`
   - Store `*exec.Cmd` for cleanup; kill process on test binary exit
   - On Linux, set `SysProcAttr.Pdeathsig = syscall.SIGTERM` for automatic cleanup

2. **Create `registry/testutil/emulatetest/seed.go`** — the seed builder API:
   - `Server.Seed() *SeedClient` returns a client pre-configured with base URL and auth
   - Response types (thin structs): `Repo{ID, FullName}`, `Release{ID, TagName, UploadURL}`, `Asset{ID, Name, Size, ContentType}`
   - `CreateRepo(owner, name string) (*Repo, error)` — POST `/orgs/:owner/repos` with `{"name": name}`. Create the org first via POST `/admin/orgs` if needed, or use POST `/user/repos` and ignore the owner (emulate auto-assigns to `admin` user).
   - `CreateRelease(owner, repo, tag string) (*Release, error)` — POST `/repos/:owner/:repo/releases` with `{"tag_name": tag}`
   - `UploadReleaseAsset(owner, repo string, releaseID int, filename string, data []byte) (*Asset, error)` — POST to `upload_url?name=<filename>` with `Content-Type: application/octet-stream` body
   - `SeedPackageRelease(owner, repo, tag, pkgDir string) (*SeedResult, error)` — high-level helper:
     a. Call `packaging.Pack(pkgDir, t.TempDir())` to produce archive + manifest
     b. Read the archive and manifest bytes
     c. CreateRepo (ignore already-exists)
     d. CreateRelease with tag
     e. Upload archive as `<name>.toolbox.pkg`
     f. Upload manifest as `toolbox.pkg.json`
     g. Return `SeedResult{Owner, Repo, Tag, ReleaseID, ArchiveAssetID, ManifestAssetID, ArchiveBytes, ManifestBytes}`
   - Store uploaded asset bytes in `SeedResult` — downstream S05 needs these for its download workaround

3. **Create `registry/testutil/emulatetest/emulatetest_test.go`** — integration tests:
   - `TestEmulateLifecycle`: Start emulate, verify `BaseURL()` returns non-empty, verify `GET /rate_limit` returns 200
   - `TestSeedRepoAndRelease`: Create repo, verify 201; create release with tag "v1.0.0", verify release ID > 0
   - `TestSeedPackageRelease`: Call `SeedPackageRelease` with `testutil/fixtures/toolbox.pkgs/calc` as source dir. Verify:
     a. No error returned
     b. `GET /repos/:owner/:repo/releases/tags/v1.0.0` returns release with 2 assets
     c. Assets have names `calc.toolbox.pkg` and `toolbox.pkg.json`
     d. Asset sizes are > 0
   - Use `google/go-cmp` for struct comparisons where appropriate

## Must-Haves

- [ ] `emulatetest.Start(t)` starts emulate once per binary and returns `*Server` with `BaseURL()` and `Client()`
- [ ] `SeedClient` can create repos, releases, and upload assets via emulate's API
- [ ] `SeedPackageRelease` produces real `.toolbox.pkg` archives via `packaging.Pack()` and uploads them
- [ ] `SeedResult` includes uploaded asset bytes for downstream workaround
- [ ] Integration test verifies full lifecycle: start → create repo → create release → upload assets → verify release-by-tag
- [ ] Graceful skip when `npx` is not available
- [ ] Known limitation (asset download broken in emulate) documented in code comment

## Verification

- `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` passes with all tests green
- Test output shows emulate starting on a port, HTTP calls succeeding, and release metadata verified

## Observability Impact

- Signals added: emulate stderr captured in buffer and logged on startup failure; HTTP error responses include status code and body
- How a future agent inspects this: run tests with `-v` flag; startup failures show captured emulate stderr; seed API failures show HTTP status + body
- Failure state exposed: emulate startup timeout message includes the captured stderr buffer contents
  - Estimate: 2h
  - Files: registry/testutil/emulatetest/emulatetest.go, registry/testutil/emulatetest/seed.go, registry/testutil/emulatetest/emulatetest_test.go
  - Verify: cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s
- [x] **T02: Built local tagged git fixture helpers with clone-based verification.** — ## Description

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
  - Estimate: 45m
  - Files: registry/testutil/gitfixture/gitfixture.go, registry/testutil/gitfixture/gitfixture_test.go
  - Verify: cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/gitfixture/ -v -count=1 -timeout 30s
- [x] **T03: Updated CI to install Node.js 22 and pre-install emulate so the registry integration suite runs in GitHub Actions.** — ## Description

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
  - Estimate: 30m
  - Files: .github/workflows/ci.yml
  - Verify: cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'setup-node' .github/workflows/ci.yml && grep -q 'emulate' .github/workflows/ci.yml && go test ./registry/... -v -count=1 -timeout 60s
