---
estimated_steps: 72
estimated_files: 3
skills_used: []
---

# T01: Build emulate lifecycle manager and seed builder with happy-path integration test

## Description

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

## Inputs

- ``packaging/packaging.go` — Pack() function to produce real .toolbox.pkg archives`
- ``packaging/internal/archive/archive.go` — ArchiveExtension constant and PackResult type`
- ``testutil/fixtures/toolbox.pkgs/calc/` — source package directory for seeding`
- ``testutil/fixtures/fixtures.go` — SourceDirs() for discovering fixture packages`

## Expected Output

- ``registry/testutil/emulatetest/emulatetest.go` — lifecycle manager with Start(), Server, BaseURL(), Client()`
- ``registry/testutil/emulatetest/seed.go` — SeedClient with CreateRepo, CreateRelease, UploadReleaseAsset, SeedPackageRelease`
- ``registry/testutil/emulatetest/emulatetest_test.go` — integration tests proving full lifecycle`

## Verification

cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s
