# S05 — Research

**Date:** 2026-03-27

## Summary

S05 defines the `PackageSource` interface and implements `GitHubReleaseSource` — the primary resolver that fetches `.toolbox.pkg` and `toolbox.pkg.json` from GitHub Release assets. The RFC (§2) specifies the contract: given `(module_path, version)`, return `(archive_bytes, manifest_bytes)` or "not found". The implementation is straightforward HTTP against the GitHub Releases API with `GITHUB_BASE_URL` override for emulate testing. The emulate binary download bug (returns JSON instead of binary) means tests must use `SeedResult.ArchiveBytes` for verification rather than downloading from emulate — this is the main integration risk.

The `PackageSource` interface should be minimal: a single `Fetch(ctx, module, version) → (archive, manifest, error)` method. `GitHubReleaseSource` derives `owner/repo` from the module path, calls `GET /repos/{owner}/{repo}/releases/tags/{version}`, finds the two expected assets, downloads them via the asset download endpoint, and returns the bytes. The Cache (S04) sits above this — the resolver checks cache first, then calls the source, then caches the result. That orchestration layer belongs in S08, not here.

## Recommendation

Build in two tasks: (1) define the `PackageSource` interface + `GitHubReleaseSource` implementation in `registry/source.go`, (2) write integration tests using emulate fixtures from S01/S02 in `registry/source_test.go`. The interface should live in the `registry` package since that's where all consumers are. Keep the interface minimal — single `Fetch` method returning raw bytes. The orchestration (cache-check → fetch → cache-store) is S08's job.

For the emulate asset download bug: tests should seed a release via `SeedPackageRelease`, then call `GitHubReleaseSource.Fetch()`. Since emulate doesn't return binary bytes correctly on asset download, the test strategy has two options: (a) use a test HTTP handler that serves the known `SeedResult.ArchiveBytes` at the asset URL, or (b) test the real HTTP flow and accept that the emulate workaround means we verify the API call sequence and URL construction rather than end-to-end bytes. Option (b) is pragmatic — verify URL construction and response parsing with emulate, and verify byte integrity through the cache round-trip tests already in S04.

## Implementation Landscape

### Key Files

- `registry/source.go` (NEW) — `PackageSource` interface and `GitHubReleaseSource` struct. Interface: `Fetch(ctx context.Context, module ModulePath, version Version) (archive []byte, manifest []byte, err error)`. GitHubReleaseSource needs: baseURL (string, defaults to `https://api.github.com`), httpClient (*http.Client, for auth token injection).
- `registry/source_test.go` (NEW) — Integration tests using `emulatetest.Start(t)` and `SeedPackageRelease`. Tests: happy-path fetch, release-not-found (404), missing archive asset, missing manifest asset, empty release (no assets).
- `registry/cache.go` — Existing, unchanged. Consumed by S08 orchestration.
- `registry/testutil/emulatetest/emulatetest.go` — Existing. Provides `Start(t)` → `*Server` with `BaseURL()` and `Client()`.
- `registry/testutil/emulatetest/seed.go` — Existing. Provides `SeedPackageRelease`, `SeedMissingAssetRelease`, `SeedEmptyRelease` for test setup.
- `tool/fqn.go` — Existing. `ModulePath` type; source derives owner/repo by splitting on `/` segments (e.g. `github.com/owner/repo` → owner=`owner`, repo=`repo`).

### Build Order

1. **T01: PackageSource interface + GitHubReleaseSource** — Define the interface, implement the GitHub REST API client. This is the core deliverable. Derives owner/repo from module path segments (segments 1 and 2 after the host). Uses `GET /repos/{owner}/{repo}/releases/tags/v{version}` to find the release, then downloads assets by name matching (`*.toolbox.pkg` and `toolbox.pkg.json`). Supports `GITHUB_TOKEN` via the http.Client's transport (same pattern as emulatetest's `authTransport`).

2. **T02: Integration tests** — Uses emulatetest to seed releases and verify GitHubReleaseSource.Fetch() against them. Tests the happy path, 404 (unknown version), and error scenarios using S02's failure builders. Given the emulate binary download limitation, the happy-path test should verify: correct API calls are made, release is found, asset names are identified. For byte-level verification, test that the fetched bytes match what was seeded (if emulate returns them correctly) or document the limitation.

### Verification Approach

- `go test ./registry/... -v -count=1 -run TestGitHubReleaseSource -timeout 60s` — all subtests pass
- `go vet ./registry/...` — clean
- Interface satisfies requirement R003: given module+version, fetch .toolbox.pkg and toolbox.pkg.json from GitHub Release assets with GITHUB_BASE_URL override

## Constraints

- Module path → GitHub owner/repo derivation: `github.com/{owner}/{repo}` maps directly. Non-GitHub hosts are out of scope for this source (git-source fallback in S06 handles those).
- `emulate` v0.3.0 asset download returns JSON metadata instead of binary bytes. The S01 summary documents this: "emulate v0.3.0 asset binary download returns JSON instead of binary — documented in code, workaround via SeedResult.ArchiveBytes for S05." Tests must account for this.
- The `http.Client` pattern from emulatetest (authTransport wrapping base transport to inject Bearer token) should be reused for the source's auth mechanism.

## Common Pitfalls

- **Asset name matching** — GitHub releases can have arbitrary asset names. Match on suffix (`.toolbox.pkg`) and exact name (`toolbox.pkg.json`) rather than assuming fixed prefixes, since the archive name includes the package name (e.g. `calc.toolbox.pkg`).
- **Asset download URL** — GitHub API returns assets with a `browser_download_url` for public repos and requires `Accept: application/octet-stream` on the API URL (`/repos/.../releases/assets/{id}`) for private repos. Use the API URL + octet-stream accept header for consistency.
- **emulate binary download bug** — Don't assume `GET /repos/.../releases/assets/{id}` returns binary from emulate. The test may need to accept the JSON response and verify the request was made correctly, or use a separate verification path.

## Open Risks

- The emulate asset download bug may make it impossible to do true end-to-end byte verification in tests. If so, the happy-path test verifies API call correctness while byte integrity is proven by S04's cache tests using the same `packaging.LoadArchive` path.
