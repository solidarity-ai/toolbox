---
id: S03
parent: M001-zku9aj
milestone: M001-zku9aj
provides:
  - ModulePath, Version, ToolPath, ToolFQN, PackageVer types with Parse/String/validation in tool package
requires:
  []
affects:
  - S04
  - S05
  - S06
  - S07
  - S08
  - S09
key_files:
  - tool/fqn.go
  - tool/fqn_test.go
key_decisions:
  - Treat digit-prefixed v0.0.0-... versions as pseudo-version candidates that must match the exact Go pseudo-version shape rather than falling through to generic semver prerelease parsing.
patterns_established:
  - Named string types for identity values (ModulePath, Version, ToolPath) with Parse*/String() round-trip pattern in the tool package.
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S03/tasks/T01-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-27T15:45:08.070Z
blocker_discovered: false
---

# S03: FQN types and parsing

**Typed FQN identity types (ModulePath, Version, ToolPath, ToolFQN, PackageVer) with parsing, validation, formatting, round-trip fidelity, and pseudo-version decomposition.**

## What Happened

Implemented `tool/fqn.go` with five identity types: `ModulePath` (host/path validated), `Version` (semver with prerelease/build metadata + Go-style pseudo-versions), `ToolPath` (dot-separated segments), `ToolFQN` (struct combining module@version/toolpath), and `PackageVer` (struct combining module@version). Each type has a `Parse*` constructor that validates format and a `String()` method for round-trip formatting. Version includes `IsPseudo()`, `PseudoTimestamp()`, and `PseudoCommit()` helpers. Added `tool/fqn_test.go` with comprehensive table-driven tests covering valid/invalid parsing, round-trip fidelity, pseudo-version extraction, and edge cases (empty strings, missing delimiters, single-segment paths). During implementation, tightened pseudo-version detection so digit-prefixed `v0.0.0-...` strings must match the exact Go pseudo-version shape rather than falling through to generic semver prerelease acceptance.

## Verification

All verification passed: `go test ./tool/... -v -count=1 -run TestFQN` (exit 0, all subtests green), `go vet ./tool/...` (exit 0, no findings).

## Requirements Advanced

- R001 — All FQN identity types implemented with parsing, formatting, validation, round-trip fidelity, and pseudo-version support.

## Requirements Validated

- R001 — Table-driven tests prove parse/format round-trips, semver and pseudo-version acceptance, and rejection of invalid inputs.

## New Requirements Surfaced

None.

## Requirements Invalidated or Re-scoped

None.

## Deviations

None.

## Known Limitations

None. All planned types and validation rules are implemented.

## Follow-ups

None.

## Files Created/Modified

- `tool/fqn.go` — FQN identity types, Parse* constructors, String() methods, pseudo-version helpers
- `tool/fqn_test.go` — Table-driven tests for all parsers, round-trip, pseudo-version extraction, edge cases
