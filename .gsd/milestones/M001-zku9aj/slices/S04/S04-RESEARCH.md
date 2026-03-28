# S04 — Research

**Date:** 2026-03-27

## Summary

S04 implements the local cache layout at `~/.cache/toolbox/pkg/` as specified in RFC §3. The cache stores resolved packages keyed by module path + version with three file types per version: `.manifest` (toolbox.pkg.json), `.pkg` (archive), and `.info` (version metadata). The layout mirrors the proxy protocol path structure (`<module>/@v/<version>.<ext>`). This is straightforward filesystem code — no new dependencies, no complex algorithms, no unfamiliar technology.

The slice needs a `Cache` type with methods to check for cached entries, store new entries, and load cached packages via `packaging.LoadArchive`. It must support `TOOLBOX_CACHE_DIR` override for testing/CI. The existing `os.UserCacheDir()` provides the platform-correct default base.

## Recommendation

Implement a single `registry/cache.go` file with a `Cache` struct holding a root directory path. Provide `NewCache(dir string)` constructor (empty string → default), `Has(module, version)`, `Put(module, version, archiveBytes, manifestBytes)`, `LoadArchive(module, version) (packaging.LoadedPackage, error)`, and path helpers. Use `tool.ModulePath` and `tool.Version` types from S03 for type safety. Test with temp directories — no external dependencies needed.

## Implementation Landscape

### Key Files

- `tool/fqn.go` — Provides `ModulePath` and `Version` types that the cache API should accept
- `packaging/packaging.go` — `LoadArchive(archivePath, manifestPath)` is called to load cached packages; returns `LoadedPackage`
- `registry/` — Currently has only `README.md` and `testutil/`. Cache code goes here as `registry/cache.go`
- `secrets/local.go` — Shows the existing XDG/cache-dir pattern in the codebase (reference for convention)

### Build Order

1. Implement `registry/cache.go` with `Cache` struct, path computation, `Put`, `Has`, `LoadArchive` methods
2. Implement `registry/cache_test.go` with table-driven tests: store+load round-trip, cache hit/miss, corrupt detection via LoadArchive's built-in sha256 check, `TOOLBOX_CACHE_DIR` override, path structure verification

### Verification Approach

- `go test ./registry/... -v -count=1 -run TestCache` — all cache tests pass
- `go vet ./registry/...` — no issues
- Verify that stored files land at the expected `<module>/@v/<version>.<ext>` paths

## Constraints

- Cache must use `packaging.LoadArchive` for loading — no reimplementation of sha256 verification
- Must accept `tool.ModulePath` and `tool.Version` types (from S03) for type safety
- Layout must match RFC §3: `<root>/pkg/<module>/@v/<version>.{manifest,pkg,info}`
- Must support `TOOLBOX_CACHE_DIR` env var override for testing and CI
