# assets

## Purpose

`assets` resolves and caches non-tool executable/package assets.

Examples:
- WASM binaries shipped alongside a package
- auxiliary artifacts needed by runtimes or tool host imports

This keeps runtime packages from needing to understand package layout or acquisition details.

## Who depends on this package

### `registry`
Provides package-backed asset material to `assets`.

### `runtime/quickjs`
Uses `assets` to resolve executable assets referenced by host imports such as `exec(...)`.

### `runtime/wasix`
Uses `assets` to locate native WASM modules or related execution artifacts.

## What they use it for

- asset lookup by name/reference
- cached local access to executable artifacts
- separating package layout concerns from runtime concerns

## What this package does not own

- package fetching
- tool definitions
- request-scoped bindings
- runtime orchestration
