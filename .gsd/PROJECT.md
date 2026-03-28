# Toolbox

## What This Is

Toolbox is a platform for giving agents better tools — structured tool definitions, request-scoped toolsets with bindings and credentials, centralized execution semantics, and code mode. It supports TypeScript tools (via QuickJS) and native WASM tools (via a Rust WASIX host), with local and hosted execution paths.

This milestone adds **package registry, FQN identity, and auto-download** — the plumbing that lets harness authors reference packages by module path + version and have them automatically fetched, cached, verified, and loaded.

## Core Value

A harness author writes a toolset referencing `zendesk@v2.0.1` and it just works — fetched, cached, integrity-verified, and loaded into a ResolvedToolset. No manual file staging.

## Current State

The core package model is built: `tool.Package` defines static tool/package types, `toolset.Builder` assembles request-scoped toolsets from `AddFromDir` (local source), `AddFromArchive` (built .toolbox.pkg), and now `AddFromRegistry` (registry resolution). `packaging.Pack` produces archives with sha256 integrity, `packaging.LoadArchive` verifies and loads them. The `registry/` package includes a cache layout, a GitHub Releases source, a git-source fallback, pseudo-version git resolution, and a `Resolver` that orchestrates cache-first resolution with ordered source fallback. Declarative `toolbox.toolset.json` loading is now implemented through `toolset.Load(filename)` plus `(*ToolsetFile).Resolve(ctx, resolver)`, with schema-backed shape validation, semantic package/tool cross-checks, and registry-backed resolution that reuses the same builder/cache path as imperative assembly. Lockfile generation, replace directives, and CLI flows remain pending.

GitHub-related testing for this milestone is constrained to local validation, emulate-backed integration tests, CI/workflow configuration changes, and other read-only checks. Pushing to `main` is not part of the test strategy.

## Architecture / Key Patterns

- **Go monorepo** with workspace (`go.work`) — main module `github.com/solidarity-ai/toolbox`
- **Package boundaries:** `tool` (static defs) → `toolset` (request-scoped assembly) → `invoke` (execution) → runtimes
- **Packaging:** `packaging/internal/source` loads dev packages, `packaging/internal/archive` produces/loads .toolbox.pkg archives with tar+zstd+sha256
- **Runtimes:** `runtime/quickts` (TypeScript via QuickJS), `runtime/tswasmcli` (TS+WASM via Rust host)
- **Testing:** `testutil/` provides fixtures, `testutil/fixtures/toolbox.pkgs/` has test packages

## Capability Contract

See `.gsd/REQUIREMENTS.md` for the explicit capability contract, requirement status, and coverage mapping.

## Milestone Sequence

- [ ] M001-zku9aj: Tool Registry, FQN, and Auto-Download — Package resolution from GitHub Releases and git-source, local cache, declarative toolset file, lockfile, CLI
