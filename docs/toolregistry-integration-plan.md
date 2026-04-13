# Toolregistry Integration Plan

## Status

Draft.

This document plans how Toolbox should consume the `toolregistry` service at
`~/dev/include-tools/toolregistry` as a primary remote package source, while
keeping the current GitHub-release and git fallback paths intact.

## Summary

Toolbox should add a new HTTP-backed registry source that speaks the
`toolregistry` proxy protocol under `/v1/packages/*`.

Initial source order:

1. local cache
2. `ToolRegistrySource` using `TOOLBOX_REGISTRY` or the hosted default
3. `GitHubReleaseSource`
4. `GitSourceFallback`

The key policy choice for phase 1 is:

- fall back from `ToolRegistrySource` to GitHub Releases when the registry is
  unreachable or unhealthy
- also fall back on a registry `404`

That keeps both "registry is down" and "registry has not indexed this release
yet" from breaking installs, while still surfacing malformed registry responses
and protected deployments as explicit errors.

## What Exists Today

Toolbox currently resolves remote packages through:

- `GitHubReleaseSource` for tagged release assets
- `GitSourceFallback` for tag/pseudo-version git checkout + local pack

Relevant code:

- [`registry/resolver.go`](/Users/mackross/dev/toolbox/registry/resolver.go)
- [`registry/source.go`](/Users/mackross/dev/toolbox/registry/source.go)
- [`registry/git_source.go`](/Users/mackross/dev/toolbox/registry/git_source.go)
- [`cmd/toolbox/main.go`](/Users/mackross/dev/toolbox/cmd/toolbox/main.go)

Current resolver behavior only skips to the next source on
`ErrReleaseNotFound`. Any other source error aborts the chain.

## What `toolregistry` Exposes

`toolregistry` is not a shared library for Toolbox to import. It is an
edge-deployed HTTP service with the API shape Toolbox already wants.

Important endpoints:

- `GET /v1/packages/{module}/@latest`
  returns typed metadata for the current latest version
- `GET /v1/packages/{module}/@v/{version}.info`
  returns typed metadata for a concrete version
- `GET /v1/packages/{module}/@v/{version}.pkg`
  returns `302` to the archive asset URL
- `GET /v1/packages/{module}/@v/{version}.manifest`
  returns `302` to the manifest asset URL
- `GET /v1/packages/{module}/@v/list`
  returns newline-delimited version strings, newest-first

Relevant files from `toolregistry`:

- `src/routes/api.ts`
- `src/db/types.ts`
- `tests/contract/rfc-protocol.test.ts`
- `OUTCOMES.md`

Important observed wire-contract details:

- `.info` returns:
  - `module`
  - `version`
  - `name`
  - `runtime`
  - `published`
  - `archive_sha256`
  - `git_sha`
  - `archive_url`
  - `manifest_url`
  - `archive_size`
  - `release_url`
  - `tools`
  - `manifest_json`
- `.pkg` and `.manifest` redirect to public GitHub release asset URLs
- `@v/list` is already in the Go-module-proxy style Toolbox wants
- the registry stores metadata and redirects, but does not host bytes itself

## Proposed Toolbox Changes

### 1. Add `ToolRegistrySource`

Add a new source in `registry`, likely:

- `registry/toolregistry_source.go`

It should implement:

- `PackageSource`
- `VersionSource`

Responsibilities:

- build proxy URLs from module path + version
- fetch `.info`
- fetch archive bytes through `.pkg`
- fetch manifest bytes through `.manifest`
- map proxy response data into `FetchResult`
- list versions through `@v/list`

### 2. Add a new provenance value

Today `ResolvedFrom` only supports:

- `github-release`
- `git-source`

Add:

- `tool-registry`

This value should be written into lockfiles when the registry source is the one
that supplied the metadata and artifact locations.

That requires updating:

- [`registry/source.go`](/Users/mackross/dev/toolbox/registry/source.go)
- [`toolsetfile/toolset_lock.go`](/Users/mackross/dev/toolbox/toolsetfile/toolset_lock.go)
- any tests validating `resolved_from`

### 3. Add `ErrSourceUnavailable`

The resolver needs a second skippable error class besides
`ErrReleaseNotFound`.

Add something like:

- `ErrSourceUnavailable`

Resolver behavior should become:

- skip to next source on `ErrReleaseNotFound`
- skip to next source on `ErrSourceUnavailable`
- abort immediately on any other error

This change applies to both:

- `Resolver.fetchFromSources`
- `Resolver.ListVersions`

### 4. Wire registry support into `newResolver()`

Add a registry env var:

- `TOOLBOX_REGISTRY`

Suggested phase 1 behavior:

- unset or empty: use `https://packages.include.tools`
- `off`: explicitly disable the registry source
- any host or URL: enable `ToolRegistrySource` with that base URL
- if the value has no scheme, normalize it to `https://<value>`

Proposed resolver construction:

1. cache
2. registry source unless explicitly disabled
3. GitHub release source
4. git fallback

This should live behind the current resolver constructor in
[`cmd/toolbox/main.go`](/Users/mackross/dev/toolbox/cmd/toolbox/main.go), so
the CLI, `sdkbridge`, and MCP paths all pick it up together.

## Exact Fetch Behavior

For `Resolve(module, version)` through the proxy:

1. `GET /v1/packages/{module}/@v/{version}.info`
2. parse and validate:
   - `archive_sha256`
   - `git_sha`
   - `archive_url`
   - `manifest_url`
3. `GET /v1/packages/{module}/@v/{version}.pkg` and follow redirects
4. `GET /v1/packages/{module}/@v/{version}.manifest` and follow redirects
5. compute the downloaded archive sha256 and compare it with
   `.info.archive_sha256`
6. compare downloaded manifest bytes with `.info.manifest_json`
7. return `FetchResult` with:
   - archive bytes
   - manifest bytes
   - `ResolvedFrom: tool-registry`

The manifest-byte comparison matters because `toolregistry` explicitly treats
`manifest_json` as an opaque verbatim payload. Toolbox should take advantage of
that instead of trusting only the redirect target.

## Exact Version Listing Behavior

For `ListVersions(module)` through the proxy:

1. `GET /v1/packages/{module}/@v/list`
2. parse newline-delimited versions
3. validate each version with existing `tool.ParseVersion`
4. sort or preserve newest-first as returned

Toolbox does not need the richer `/versions` response in phase 1.

## Fallback Policy

### Fallback cases in phase 1

`ToolRegistrySource` should return `ErrReleaseNotFound` for:

- `404`

`ToolRegistrySource` should return `ErrSourceUnavailable` for:

- transport errors from `http.Client.Do`
- context deadline / timeout while contacting the proxy
- HTTP `5xx`
- Cloudflare-style upstream availability errors such as `520`, `522`, `523`,
  and `524`
- malformed `.info` JSON
- valid JSON missing required fields for resolution
- module/version mismatch in `.info`
- archive sha mismatch after download
- manifest bytes that do not match `.info.manifest_json`

These are all "the proxy cannot currently act as a trustworthy source"
conditions, so falling back to GitHub Releases is the right behavior.

### Non-fallback cases in phase 1

`ToolRegistrySource` should not fall back automatically on:

- `400`
- `401`
- `403`

Those should remain hard errors in phase 1.

Rationale:

- `400` means Toolbox constructed a bad request
- `401` / `403` mean misconfiguration or an unexpected protected deployment

## Testing Plan

### Toolbox unit tests

- `ToolRegistrySource.Fetch` against an `httptest` server
- `ToolRegistrySource.ListVersions` against an `httptest` server
- resolver fallback on `ErrSourceUnavailable`
- resolver fallback on proxy `404`

### Integration-style Toolbox tests

- proxy source success path
- proxy timeout then GitHub release fallback
- proxy malformed `.info` then GitHub release fallback
- proxy `404` then GitHub release fallback
- version listing through proxy

### Toolregistry contract alignment

Pin a minimal set of expectations in Toolbox tests to the parts Toolbox uses:

- `.info` field names
- `.pkg` / `.manifest` redirect behavior
- `@v/list` format

## Recommended Rollout

### Phase 1

- add `ToolRegistrySource`
- add `ErrSourceUnavailable`
- add `tool-registry` provenance
- wire `TOOLBOX_REGISTRY`
- default it to `https://packages.include.tools`

### Phase 2

- optionally add a direct `LatestVersion` resolver path using `@latest`

## Non-Goals for This First Pass

- replacing GitHub release resolution completely
- changing package identity away from module path
- using the HTML/search routes from Toolbox resolution code
- making the proxy mandatory
