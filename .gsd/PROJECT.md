# Toolbox

## What This Is

Toolbox is a platform for giving agents better tools — structured tool definitions, request-scoped toolsets with bindings and credentials, centralized execution semantics, and code mode. It supports TypeScript tools (via QuickJS) and native WASM tools (via a Rust WASIX host), with local and hosted execution paths.

This milestone adds **package registry, FQN identity, and auto-download** — the plumbing that lets harness authors reference packages by module path + version and have them automatically fetched, cached, verified, and loaded.

## Core Value

A harness author writes a toolset referencing `zendesk@v2.0.1` and it just works — fetched, cached, integrity-verified, and loaded into a ResolvedToolset. No manual file staging.

## Current State

The core package model is built: `tool.Package` defines static tool/package types, `toolset.Builder` assembles request-scoped toolsets from `AddFromDir` (local source), `AddFromArchive` (built .toolbox.pkg), and `AddFromRegistry` (registry resolution). `packaging.Pack` produces archives with sha256 integrity, `packaging.LoadArchive` verifies and loads them. The `registry/` package includes a cache layout, a GitHub Releases source, a git-source fallback, pseudo-version git resolution, a `Resolver` that orchestrates cache-first resolution with ordered source fallback, and version discovery for both GitHub releases and git tags. Declarative `toolbox.toolset.json` loading, lockfiles, and local replace overlays now live in the dedicated `toolsetfile/` package. The unified `cmd/toolbox` entrypoint ships the M001 surface: `toolbox resolve`, `toolbox versions`, single-module `toolbox resolve --upgrade`, and `--file` selection, all proven through CLI-boundary tests, committed-lock cache-hit re-resolve, sibling local-overlay behavior, token-auth header wiring without token leakage, and full-repo closeout verification. M001 is complete.

GitHub-related testing for this milestone keeps emulate-backed integration tests and local/CI validation as the baseline. A real GitHub account remains an optional higher-fidelity UAT path only on a non-`main` branch or disposable repository with explicit user approval; pushing to `main` remains out of bounds.

## Architecture / Key Patterns

- **Go monorepo** with workspace (`go.work`) — main module `github.com/solidarity-ai/toolbox`
- **Package boundaries:** `tool` (static defs) → `toolset` (request-scoped assembly) → `toolsetfile` (declarative file/lock/overlay concerns) → `invoke` (execution) → runtimes
- **Packaging:** `packaging/internal/source` loads dev packages, `packaging/internal/archive` produces/loads .toolbox.pkg archives with tar+zstd+sha256
- **Runtimes:** `runtime/quickts` (TypeScript via QuickJS), `runtime/tswasmcli` (TS+WASM via Rust host)
- **Testing:** `testutil/` provides fixtures, `testutil/fixtures/toolbox.pkgs/` has test packages

## Capability Contract

See `.gsd/REQUIREMENTS.md` for the explicit capability contract, requirement status, and coverage mapping.

## Milestone Sequence

- [x] M001-zku9aj: Tool Registry, FQN, and Auto-Download — Package resolution from GitHub Releases and git-source, local cache, declarative toolset file, lockfile, CLI
