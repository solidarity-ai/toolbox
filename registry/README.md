# registry

## Purpose

`registry` resolves module-path package references into cached, verified package
artifacts plus provenance metadata.

It owns:
- package reference parsing/resolution
- git/tag fetch logic
- local cache layout
- provenance/integrity verification for cached or fetched registry packages
- version listing

`registry` should return resolved package artifacts and metadata, not
request-scoped behavior.

## Who depends on this package

### `assembler`
Uses `registry.Resolver` to resolve registry-backed package declarations into
loaded packages and provenance metadata.

### `toolsetfile`
Passes a `registry.Resolver` into `assembler.Load` through the declarative
toolset resolution flow.

### `cmd/toolbox`
Constructs the concrete resolver used by CLI commands.

## What they use it for

- fetching a package at a pinned module path + version
- verifying cached or fetched registry metadata
- listing available versions
- reusing cached results across repeated resolutions

## What this package does not own

- local replacement directories
- explicit archive-path declarations
- multi-package assembly/order
- request-scoped bindings
- execution semantics
- HTTP transport
- code mode behavior
