# toolsetfile

## Purpose

`toolsetfile` owns the file formats and resolution flow for declarative
toolsets.

It covers three sibling files:
- `*.toolset.json`
- `*.toolset.local.json`
- `*.toolset.lock`

The package validates these files, derives sibling filenames, combines their
information into an `assembler.Declaration`, calls `assembler.Load`, then calls
`toolset.ResolveTools`.

## File roles

### `*.toolset.json`
The declared toolset contract.

Fields:
- `packages`: map of module path to exact version
- `tools`: ordered list of fully-qualified tool references

Constraints:
- every tool must reference a declared package
- every tool version must match the declared package version
- tool order is preserved

### `*.toolset.local.json`
Optional local-only overlay.

Fields:
- `replace`: map of module path to replacement directory

Purpose:
- redirect one declared package to a local source directory
- keep those overrides out of the main toolset file

Relative replacement paths are resolved relative to the overlay file location.

### `*.toolset.lock`
Resolved provenance for registry-loaded packages.

Fields per package key (`{module}@{version}`):
- `archive_sha256`
- `git_sha`
- `resolved_from`
- `resolved_at`

This file records what registry resolution trusted. It is not used for local
replacement packages.

## Resolution flow

`ToolsetFile.Resolve(ctx, resolver, cfg...)` does this:

1. Validates that the main toolset file has already been loaded.
2. Loads the optional sibling lockfile if present.
3. Loads the optional sibling local overlay if present.
4. Builds an `assembler.Declaration` from declared packages plus overlay and
   lock metadata.
5. Calls `assembler.Load`.
6. Calls `toolset.ResolveTools` with the loaded tools and optional config.
7. Rewrites the lockfile only after all packages resolve successfully.

## Important combinations

### Declared package only

- No local replace
- No lock entry
- Result: package resolves through `registry.Resolver`

### Declared package + lock entry

- No local replace
- Existing lock metadata present
- Result: package resolves through `registry.Resolver` with expected
  provenance/integrity

### Declared package + local replace

- Local overlay provides `replace[module]`
- Result: package loads from the local source directory and skips registry
  resolution

### Local replace + existing lock entry

- Local replacement wins for loading
- Existing lock entry is preserved
- Result: no new registry metadata is derived from the local package

### Replaced-only package

- Declared package resolved only from local replacement
- Result: no new lock entry is created for that package

### Invalid overlay or failed replacement load

- Malformed overlay JSON/schema/semantics, or replacement path fails to load
- Result: resolution aborts before lockfile mutation

## What this package does not own

- remote fetch logic and cache policy
- package-set loading mechanics beyond building the declaration
- binding semantics
- runtime execution
