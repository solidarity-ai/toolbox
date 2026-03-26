# S01 — Research: Emulate lifecycle + happy-path fixtures

**Date:** 2026-03-26  
**Depth:** Deep (unfamiliar integration, highest-risk item, novel subprocess lifecycle)

## Summary

This slice builds the shared Go test infrastructure that all downstream resolver slices (S05, S06, S07, S08) depend on. The core challenge is managing a Node.js subprocess (vercel-labs/emulate) from Go tests, seeding it with repos and release assets via the GitHub REST API, and also creating local bare git repos at tags for git-source fallback testing.

Live testing against emulate v0.3.0 confirmed: (1) emulate starts reliably via `npx emulate start --service github --port <N>`, (2) auth works out of the box with `Authorization: token ghp_test123` and an auto-created `admin` user, (3) repo creation + release creation + asset upload all work via the standard GitHub REST API, (4) release-by-tag lookup returns correct metadata and assets. However, **asset binary download does not work** — the `Accept: application/octet-stream` header on `GET /repos/:owner/:repo/releases/assets/:id` returns JSON metadata instead of binary content. This gap must be flagged for S05's planner. The `browser_download_url` also 404s. The fixture builder can still upload assets; the download path needs a workaround in S05.

CI will require adding `setup-node` to the GitHub Actions workflow since emulate is an npm package and the current CI config only installs Go and Rust.

## Recommendation

Build two independent fixture subsystems within `registry/testutil/`:

1. **Emulate lifecycle manager** (`emulatetest` or similar) — a Go helper that starts emulate once per test binary via `sync.Once` + `exec.Command`, waits for the port to be ready (HTTP health poll), and provides a `BaseURL()` for tests. Cleanup via `t.Cleanup` at the TestMain level or process kill. Expose a builder API for seeding repos + releases + assets via HTTP calls to the running emulate instance.

2. **Local git repo builder** (`gitfixture` or similar) — creates bare git repos in temp dirs with commits and tags. Uses `exec.Command("git", ...)` directly. Returns `file://` URLs that the git-source resolver (S06) will clone from.

Both should live in `registry/testutil/` to stay close to the registry package that consumes them. The existing `testutil/` at project root is for project-wide test helpers; registry-specific fixtures belong in the registry package tree.

## Implementation Landscape

### Key Files

- `registry/` — Currently just `README.md`. All new files go here. The `testutil/` subtree will hold the test infrastructure.
- `packaging/packaging.go` — Facade with `Pack()` and `LoadArchive()`. The fixture builder calls `Pack()` on existing fixture packages to produce real `.toolbox.pkg` + `toolbox.pkg.json` assets.
- `packaging/internal/archive/archive.go` — `Pack()` implementation producing tar+zstd archives with sha256. `LoadArchive()` verifies integrity. Both are called by fixtures.
- `testutil/fixtures/toolbox.pkgs/calc/` — Existing fixture package (simplest: 3 tools, typescript-sandbox runtime). Ideal source for happy-path release asset generation.
- `testutil/fixtures/fixtures.go` — `SourceDirs()` returns all fixture package paths. Reuse this to enumerate packages for seeding.
- `toolset/toolset.go` — `Builder` with `AddFromDir`, `AddFromArchive`, `Resolve`. Shows the pattern that `AddFromRegistry` will follow.
- `cmd/toolbox-pack/main.go` — CLI pattern for `toolbox-pack`. Shows how `Pack()` is called.
- `.github/workflows/ci.yml` — Needs `setup-node` step added for emulate.
- `go.mod` — No new Go dependencies needed. HTTP calls use stdlib `net/http`. Process management uses `os/exec`.

### Build Order

**1. Emulate lifecycle manager (highest risk, prove first)**
- `registry/testutil/emulatetest/emulatetest.go` — `Start()` function using `sync.Once` to start emulate subprocess exactly once per test binary. Polls `http://localhost:<port>/rate_limit` until ready (emulate returns 200 for this endpoint). Returns `*Server` with `BaseURL()`, `Client()` (pre-configured `http.Client`), and `Reset()` (calls emulate's reset if available, or just notes it). Uses `exec.Command("npx", "emulate", "start", "--service", "github", "--port", portStr)`.
- Port allocation: use a fixed high port (e.g., 14580) or dynamically find a free port. Fixed port is simpler and more debuggable; dynamic is more CI-safe. Recommend: use env var `EMULATE_PORT` with sensible default.

**2. Seed builder API**
- `registry/testutil/emulatetest/seed.go` — Builder for seeding test data via HTTP. Methods like:
  - `CreateRepo(owner, name) (*Repo, error)` 
  - `CreateRelease(owner, repo, tag string) (*Release, error)`
  - `UploadReleaseAsset(owner, repo string, releaseID int, name string, data []byte) (*Asset, error)`
  - `SeedPackageRelease(owner, repo, tag string, pkgDir string) error` — high-level: calls `Pack()` on pkgDir, creates release, uploads `.toolbox.pkg` + `toolbox.pkg.json` as assets
- Response types: thin structs with just the fields we need (id, tag_name, upload_url, etc.)

**3. Local git repo builder**
- `registry/testutil/gitfixture/gitfixture.go` — Creates temporary bare git repos with tagged commits containing real package source files.
  - `CreateTaggedRepo(t *testing.T, tag string, files map[string][]byte) string` — returns `file://` clone URL
  - Uses `git init --bare`, `git hash-object`, `git mktree`, `git commit-tree`, `git update-ref` to build commits without a working tree. Alternatively, simpler: `git init`, write files, `git add .`, `git commit`, `git tag`, return path.
  - Simpler approach preferred: init non-bare, write files, add/commit/tag, then the resolver can clone from the directory path.

**4. Happy-path integration test**
- `registry/testutil/emulatetest/emulatetest_test.go` — Test that proves: start emulate → create repo → create release → upload real .toolbox.pkg asset → verify release-by-tag returns asset metadata with correct sizes.
- `registry/testutil/gitfixture/gitfixture_test.go` — Test that proves: create tagged repo → `git clone file:///path` works → tag checkout has expected files.

**5. CI update**
- `.github/workflows/ci.yml` — Add `setup-node` step before Go tests.

### Verification Approach

```bash
# Run the emulate lifecycle test
cd registry/testutil/emulatetest && go test -v -run TestEmulateLifecycle -count=1

# Run the git fixture test  
cd registry/testutil/gitfixture && go test -v -run TestCreateTaggedRepo -count=1

# Run all registry tests
go test ./registry/... -count=1 -timeout 60s

# Verify CI config is valid
# (manual check: setup-node added, Go test still works)
```

Observable success:
- Emulate starts in <5s, health check passes
- `POST /user/repos` returns 201 with repo data
- `POST /repos/:owner/:repo/releases` returns release with `upload_url`
- `POST {upload_url}?name=calc.toolbox.pkg` with real archive bytes returns asset with correct `size`
- `GET /repos/:owner/:repo/releases/tags/v1.0.0` returns release with 2 assets (archive + manifest)
- Git fixture creates repo, `git tag -l` shows the tag, files are present at that tag

## Constraints

- **No new Go dependencies.** All HTTP is stdlib `net/http`, all process management is `os/exec`. The project uses `google/go-cmp` for test diffs — continue that pattern.
- **Node.js must be available.** CI needs `actions/setup-node@v4` added. Locally, `npx` must be on PATH.
- **Emulate is started once per test binary, not per test.** `sync.Once` pattern. Individual tests should be parallelizable against the shared instance.
- **Registry test files live in `registry/`**, not in the top-level `testutil/`. The `testutil/fixtures/` at root provides source packages; registry-specific test infrastructure wraps those.
- **`packaging.Pack()` is the only way to produce archives.** Don't hand-roll tar+zstd. The existing function produces archives that `LoadArchive()` can verify.

## Common Pitfalls

- **Port conflicts in CI.** If multiple test binaries run in parallel and all try the same emulate port, they'll clash. Mitigation: use a unique port per test binary (hash of package path) or run registry tests serially. The `go test ./...` command runs packages in parallel by default.
- **Emulate startup race.** The subprocess may not be ready when the first HTTP call fires. Must poll a health endpoint (e.g., `GET /rate_limit`) with retries and backoff before declaring ready.
- **npx download delay.** First run of `npx emulate` downloads the package (~seconds). In CI, this is cached across runs but cold starts will be slow. Consider pre-installing with `npm install -g emulate` in CI or using a package-lock.
- **Process cleanup on test failure.** If a test panics before cleanup runs, the emulate subprocess leaks. Use `cmd.Process.Kill()` in a `t.Cleanup()` registered before the subprocess starts, and also handle `SIGTERM`/`SIGINT` gracefully.
- **Git user config in CI.** `git commit` requires `user.name` and `user.email`. In CI, these may not be set. Use `git -c user.name=test -c user.email=test@test.com commit` or set env vars `GIT_AUTHOR_NAME`, `GIT_COMMITTER_NAME`, etc.
- **`t.TempDir()` on Windows.** Not relevant for CI (ubuntu-latest) but worth noting: git on Windows has path length limits. Use short temp dir names.

## Open Risks

- **Asset binary download does not work in emulate v0.3.0.** The `Accept: application/octet-stream` header on `GET /repos/:owner/:repo/releases/assets/:id` returns JSON metadata, not binary content. The `browser_download_url` also 404s. S05 (GitHub Releases resolver) must work around this — either by contributing a fix upstream, by running a thin Go HTTP proxy that intercepts download requests and serves stored asset bytes, or by storing asset bytes alongside the emulate seeding and serving them from a Go `httptest.Server`. **This is the single biggest risk for the milestone.** S01 should surface this clearly for S05.
- **Emulate `/repos/:owner/:repo/tags` endpoint returns empty** even after creating tag refs via the Git Data API. The `git/ref/tags/:tag` endpoint works. S06 (git-source) may need to use the refs API instead of the tags API.
- **Emulate Git Data API doesn't return tree from auto-init commits.** `GET /repos/:owner/:repo/git/commits/:sha` doesn't include the tree SHA. Tree creation from scratch (blob → tree → commit) works fine. Seeding complete file trees requires building the tree from blobs explicitly.

## Forward Intelligence for Downstream Slices

### For S02 (Failure scenario test builders)
- The seed builder from S01 provides the foundation. S02 extends it with methods that produce *broken* data: corrupt archive bytes, mismatched sha256 in manifests, missing release assets, git repos without valid package files.
- Asset upload accepts arbitrary bytes — perfect for uploading deliberately corrupt archives.

### For S05 (GitHub Releases source)
- **CRITICAL:** Asset binary download doesn't work in emulate. The resolver will need a workaround. Recommended: seed builder stores uploaded asset bytes in-memory, and a thin Go `httptest.Server` proxy sits in front of emulate — passes through metadata requests to emulate, but serves asset downloads directly from stored bytes. Alternatively, the resolver fetches asset metadata from emulate and downloads from a separate URL.
- Release-by-tag works (`GET /repos/:owner/:repo/releases/tags/:tag`). This is the entry point for the resolver.
- The `upload_url` in release responses uses the emulate base URL correctly.

### For S06 (Git-source fallback)
- Local git fixture repos created in S01 are the test subjects. Uses `file://` URLs, no network.
- Don't rely on emulate's Git Data API for full source trees — use real local git repos instead.

### For S08 (Resolver orchestration)
- Both emulate (for releases) and git fixtures (for fallback) are available. S08 tests chain: try release source → fall back to git source.

## Skills Discovered

| Technology | Skill | Status |
|------------|-------|--------|
| Go testing | affaan-m/everything-claude-code@golang-testing | available (2.6K installs) |
| Go testing | cxuu/golang-skills@go-testing | available (291 installs) |

## Sources

- Emulate lifecycle and API confirmed via live testing against emulate v0.3.0 (source: [Context7 /vercel-labs/emulate docs](https://github.com/vercel-labs/emulate))
- GitHub REST API conventions for releases and assets (source: [GitHub REST API docs](https://docs.github.com/en/rest/releases))
- RFC design authority for implementation sequence and resolver design (source: `docs/rfc-tool-registry.md` §2, §3)
