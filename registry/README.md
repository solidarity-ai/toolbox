# registry

## Purpose

`registry` acquires, caches, and materializes packages.

It owns:
- package reference parsing/resolution
- git/tag fetch logic
- local cache layout
- manifest loading
- producing loaded package artifacts for the rest of the system

`registry` should return inert loaded artifacts, not executable behavior.

## Who depends on this package

### `service`
Uses `registry` to resolve package refs or tool refs into loaded package artifacts before request-scoped assembly.

### `assets`
Uses package material loaded by `registry` to resolve actual asset blobs/paths.

## What they use it for

- fetching package source at a pinned version
- reading and validating manifests
- obtaining tool definitions and assets from a package ref
- cache-backed loading for repeated use

## What this package does not own

- request-scoped bindings
- execution semantics
- HTTP transport
- code mode behavior
