# assembler

## Purpose

`assembler` turns a package declaration into an ordered set of loaded packages
and flattened execution-ready tools.

It owns:
- the declaration types used to describe package inputs
- local source package loading via `ReplaceDir`
- explicit dist archive loading via `ArchivePath` + `ManifestPath`
- registry-backed package loading via `registry.Resolver`
- deterministic package ordering
- returning `LoadedPackages`, `LoadedPackage`, and flattened tool lists

The main entrypoint is:

```go
loaded, err := assembler.Load(ctx, resolver, decl)
```

## Who depends on this package

### `toolsetfile`
Builds an `assembler.Declaration` from `*.toolset.json` plus the local overlay
and lockfile, then calls `assembler.Load`.

### `testutil/tooltest`
Builds fixture-backed declarations for tests and uses `assembler.Load` to
materialize package fixtures before calling `toolset.ResolveTools`.

## What callers use it for

- loading one or more local source packages
- loading one or more dist packages from fixture or cache paths
- resolving registry packages with optional expected provenance
- obtaining loaded package metadata and flattened tool lists in package order

## What this package does not own

- binding compilation or evaluation
- agent-visible schemas
- call validation
- runtime execution
- lockfile rewriting
- remote fetch source policy beyond the `registry.Resolver` interface
