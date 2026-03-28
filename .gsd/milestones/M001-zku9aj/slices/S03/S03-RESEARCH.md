# S03: FQN types and parsing — Research

**Date:** 2026-03-27
**Depth:** Light — this is pure types + parsing with clear spec in the RFC.

## Summary

S03 adds FQN identity types (`ModulePath`, `Version`, `ToolFQN`) to the `tool` package and parsing/formatting functions. The RFC §1 fully specifies the format: `{module_path}@{version}/{tool_path}`. Pseudo-versions follow Go's `v0.0.0-{yyyyMMddHHmmss}-{12char_sha}` format. The existing `tool/tool.go` is 62 lines of plain structs — adding types here is clean and risk-free. No external dependencies needed.

This slice owns R001 (FQN identity foundation). Every downstream slice (S04–S12) depends on these types.

## Recommendation

Add all FQN types and parsing to `tool/tool.go` (or a new `tool/fqn.go` file for separation). Use standard library only — `strings`, `regexp`, `fmt`. Validate module paths (must have host/org/repo minimum), versions (semver with `v` prefix or pseudo-version), and tool paths (dot-separated segments). Provide `Parse*` constructors that return errors, `String()` formatters, and round-trip faithfulness.

## Implementation Landscape

### Key Files

- `tool/tool.go` — Existing types (`Package`, `ResolvedTool`). FQN types live alongside these per D002.
- `tool/fqn.go` (new) — `ModulePath`, `Version`, `ToolFQN` types with Parse/String/validation. Keep separate from tool.go for clarity.
- `tool/fqn_test.go` (new) — Table-driven tests for parsing, formatting, round-trips, error cases.

### Types to Define

From RFC §1:

```go
// ModulePath is a git-host-qualified package identity (e.g. "github.com/acme-corp/zendesk-tools")
type ModulePath string

// Version is a semver version with v prefix (e.g. "v2.0.1") or pseudo-version
type Version string

// ToolPath is a dot-separated tool resource path (e.g. "account.tickets.comments.add")
type ToolPath string

// ToolFQN is the full identity: module_path@version/tool_path
type ToolFQN struct {
    Module  ModulePath
    Version Version
    Tool    ToolPath
}

// PackageVer is module_path@version (no tool path) — used by toolset files and cache keys
type PackageVer struct {
    Module  ModulePath
    Version Version
}
```

### Parsing Rules (from RFC §1)

- **ModulePath**: Must contain at least 2 path segments (host/path). First segment must have a dot (hostname).
- **Version**: Either semver `v{major}.{minor}.{patch}` (with optional pre-release/build) or pseudo-version `v0.0.0-{14 digits}-{12 hex chars}`.
- **ToolFQN string**: Split on `@` → module path + rest. Split rest on first `/` → version + tool path.
- **PackageVer string**: Split on `@` → module path + version.

### Build Order

1. Define types + `ParseModulePath`, `ParseVersion`, `ParseToolFQN`, `ParsePackageVer`
2. Add `String()` methods for all types
3. Add `Version.IsPseudo() bool` helper (needed by S07)
4. Add `Version.PseudoTimestamp()` and `Version.PseudoCommit()` extractors (needed by S07)
5. Tests — table-driven covering valid cases, invalid cases, round-trips

### Verification Approach

```bash
go test ./tool/... -v -count=1 -run TestFQN
go vet ./tool/...
```

Round-trip property: `ParseToolFQN(fqn.String()) == fqn` for all valid inputs.

## Constraints

- FQN types must live in the `tool` package (D002) — not in `registry/` or a new package.
- Must use `ModulePath` (not raw strings) in all downstream interfaces — establishes type safety from the start.
- Pseudo-version format must exactly match Go modules: `v0.0.0-{yyyyMMddHHmmss}-{12 hex}`.
