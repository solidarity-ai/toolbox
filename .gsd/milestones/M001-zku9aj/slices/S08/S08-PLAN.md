# S08: Resolver orchestration + Builder.AddFromRegistry

**Goal:** Wire `Cache`, `GitHubReleaseSource`, and `GitSourceFallback` into a `Resolver` that orchestrates cache-check → primary-source → fallback → cache-write → load, then expose it through `Builder.AddFromRegistry` so downstream code acquires registry packages the same way as local packages.
**Demo:** After this: After this: TBD

## Tasks
- [x] **T01: Implement Resolver orchestration with cache-first source chaining and contract tests** — Add `registry/resolver.go` with a `Resolver` that owns a `*Cache` plus ordered `PackageSource` implementations. `Resolve(ctx, module, version)` should check `Cache.Has`/`LoadArchive` first, then try each source in order, continuing only when the current source returns `ErrReleaseNotFound`, otherwise failing immediately. On a successful fetch, it must write archive+manifest bytes through `Cache.Put` and return the package by calling `Cache.LoadArchive` so the same integrity path used by `AddFromArchive` is exercised. Cover this with focused tests in `registry/resolver_test.go` using mock `PackageSource` implementations plus real dist fixture bytes from `loadDistFixtureBytes` to prove cache-hit bypass, primary success with cache population, fallback on `ErrReleaseNotFound`, no fallback on non-404 source failure, and combined failure when all sources are exhausted.
  - Estimate: 1h
  - Files: registry/resolver.go, registry/resolver_test.go, registry/cache_test.go
  - Verify: GOWORK=$(pwd)/go.work go test ./registry -v -count=1 -run TestResolver -timeout 30s
  - Duration: 15m
- [x] **T02: Add Builder.AddFromRegistry with resolver injection and end-to-end builder tests** — Extend `toolset.Builder` to optionally hold a registry resolver while keeping existing `New()` behavior intact for local-only callers. Add a constructor or option such as `NewWithResolver(...)` and implement `AddFromRegistry(ctx, modulePath, version string) error` in `toolset/toolset.go`. The method must parse inputs with `tool.ParseModulePath` and `tool.ParseVersion`, return a clear error when no resolver is configured, call resolver resolution, and append the resulting `packaging.LoadedPackage` so `Packages()` and `Resolve()` behave the same as they do for `AddFromDir` and `AddFromArchive`. Add tests in `toolset/toolset_test.go` that use a pre-populated temp cache and a real dist fixture package to prove the builder can load from the registry path without network access, and that the nil-resolver path returns a specific error instead of panicking.
  - Estimate: 45m
  - Files: toolset/toolset.go, toolset/toolset_test.go, registry/resolver.go, registry/cache.go
  - Verify: GOWORK=$(pwd)/go.work go test ./toolset -v -count=1 -run TestBuilderAddFromRegistry -timeout 30s
  - Duration: 10m
