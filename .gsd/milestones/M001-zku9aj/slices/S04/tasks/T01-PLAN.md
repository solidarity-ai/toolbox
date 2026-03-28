---
estimated_steps: 1
estimated_files: 2
skills_used: []
---

# T01: Implement Cache struct with Put/Has/LoadArchive and table-driven tests

Implement `registry/cache.go` with a `Cache` struct holding a root directory. Constructor `NewCache(dir string)` defaults to `os.UserCacheDir()/toolbox/pkg` when dir is empty, and respects `TOOLBOX_CACHE_DIR` env var. Methods: `Has(module ModulePath, version Version) bool`, `Put(module ModulePath, version Version, archiveBytes, manifestBytes []byte) error`, `LoadArchive(module ModulePath, version Version) (packaging.LoadedPackage, error)`. Internal path helper computes `<root>/<module>/@v/<version>.{pkg,manifest,info}`. Put writes all three files (info can be a JSON blob with version string). LoadArchive delegates to `packaging.LoadArchive(archivePath, manifestPath)`. Then implement `registry/cache_test.go` with table-driven tests covering: (1) Put+Has round-trip, (2) Has returns false for missing entry, (3) Put+LoadArchive round-trip with a real archive from packaging test fixtures, (4) path structure verification (check files land at expected paths), (5) TOOLBOX_CACHE_DIR override, (6) LoadArchive on missing entry returns error.

## Inputs

- ``tool/fqn.go` — ModulePath and Version types from S03`
- ``packaging/packaging.go` — LoadArchive function signature`

## Expected Output

- ``registry/cache.go` — Cache struct with NewCache, Has, Put, LoadArchive methods`
- ``registry/cache_test.go` — table-driven tests for all cache operations`

## Verification

go test ./registry/... -v -count=1 -run TestCache && go vet ./registry/...
