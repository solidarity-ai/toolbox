---
estimated_steps: 15
estimated_files: 1
skills_used: []
---

# T01: Implement PackageSource interface and GitHubReleaseSource

Define the `PackageSource` interface with a single `Fetch` method and implement `GitHubReleaseSource` that resolves packages from GitHub Release assets.

The interface: `Fetch(ctx context.Context, module ModulePath, version Version) (archive []byte, manifest []byte, err error)`

`GitHubReleaseSource` struct holds `baseURL` (defaults to `https://api.github.com`) and `httpClient`. It:
1. Derives owner/repo from module path segments (e.g. `github.com/owner/repo` → owner=segments[1], repo=segments[2])
2. Calls `GET /repos/{owner}/{repo}/releases/tags/v{version}` (prepend 'v' only if version doesn't start with 'v' — but Version type already includes the 'v' prefix per S03)
3. Finds assets by name: one ending in `.toolbox.pkg` (archive) and one named `toolbox.pkg.json` (manifest)
4. Downloads each asset via `GET /repos/{owner}/{repo}/releases/assets/{id}` with `Accept: application/octet-stream`
5. Returns (archiveBytes, manifestBytes, nil) on success

Error cases to handle:
- Release not found (404) → return a sentinel or typed error
- Archive asset not found in release → descriptive error
- Manifest asset not found in release → descriptive error
- No assets at all → descriptive error
- HTTP errors during download → wrap with context

Constructor: `NewGitHubReleaseSource(baseURL string, client *http.Client) *GitHubReleaseSource`. Empty baseURL defaults to `https://api.github.com`.

## Inputs

- ``tool/fqn.go` — ModulePath and Version types used as parameters`

## Expected Output

- ``registry/source.go` — PackageSource interface and GitHubReleaseSource implementation`

## Verification

go build ./registry/... && go vet ./registry/...
