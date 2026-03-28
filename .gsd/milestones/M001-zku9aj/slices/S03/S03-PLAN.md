# S03: 

**Goal:** ---
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
  - Named string types for identity values with Parse*/String() round-trip pattern in the tool package.
observability_surfaces:
  - none
drill_down_paths:
  - .gsd/milestones/M001-zku9aj/slices/S03/tasks/T01-SUMMARY.md
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:35:58.797Z
blocker_discovered: false
---

# S03: FQN types and parsing

**Typed FQN identity types with parsing, validation, formatting, round-trip fidelity, and pseudo-version decomposition.**

## What Happened

Implemented tool/fqn.go with five identity types: ModulePath, Version, ToolPath, ToolFQN, and PackageVer. Each type has a Parse constructor that validates format and a String method for round-trip formatting. Version includes IsPseudo, PseudoTimestamp, and PseudoCommit helpers. Added tool/fqn_test.go with comprehensive table-driven tests covering valid/invalid parsing, round-trip fidelity, pseudo-version extraction, and edge cases. During implementation, tightened pseudo-version detection so digit-prefixed v0.0.0-... strings must match the exact Go pseudo-version shape rather than falling through to generic semver prerelease acceptance.

## Verification

All verification passed: `go test ./tool/... -v -count=1 -run TestFQN` and `go vet ./tool/...`.

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

- `tool/fqn.go` — FQN identity types, Parse constructors, String methods, pseudo-version helpers
- `tool/fqn_test.go` — Table-driven tests for all parsers, round-trip, pseudo-version extraction, and edge cases

**Demo:** After this: # S03: FQN types and parsing — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-28T00:35:58.798Z

# S03: FQN types and parsing — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27

## UAT Type

- UAT mode: artifact-driven
- Why this mode is sufficient: Pure types and parsers with no runtime behavior — validated entirely by unit tests.

## Preconditions

- Working directory: the M001-zku9aj worktree root
- Go toolchain available

## Smoke Test

Run `go test ./tool/... -v -count=1 -run TestFQN` — all tests pass.

## Test Cases

### 1. ModulePath parsing accepts valid host/path

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseModulePath/valid`
2. **Expected:** Passes. `github.com/owner/repo` parses successfully. `String()` returns the same value.

### 2. ModulePath rejects single-segment or no-dot paths

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseModulePath/no_dot`
2. **Expected:** Passes. Inputs like `nohost/repo` or `justasegment` return errors.

### 3. Version parsing accepts semver with prerelease and build

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/valid_semver_prerelease_build`
2. **Expected:** Passes. `v1.2.3-alpha.1+build` parses and round-trips.

### 4. Pseudo-version parsing and decomposition

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/valid_pseudo`
2. **Expected:** Passes. `v0.0.0-20260101120000-abcdef012345` parses. `IsPseudo()` returns true. `PseudoTimestamp()` returns `20260101120000`. `PseudoCommit()` returns `abcdef012345`.

### 5. Malformed pseudo-versions rejected

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseVersion/bad_pseudo`
2. **Expected:** Passes. Strings like `v0.0.0-2026-abc` return errors rather than being accepted as generic semver prereleases.

### 6. ToolFQN round-trip

1. Run `go test ./tool/... -v -count=1 -run TestFQNParseToolFQN`
2. **Expected:** Passes. `github.com/owner/repo@v1.0.0/my.tool` parses into Module+Version+Tool and `String()` reproduces the original.

### 7. PackageVer round-trip

1. Run `go test ./tool/... -v -count=1 -run TestFQNParsePackageVer`
2. **Expected:** Passes. `github.com/owner/repo@v1.0.0` parses and round-trips.

## Edge Cases

### Empty and malformed inputs

1. Run `go test ./tool/... -v -count=1 -run TestFQN` (full suite covers empty strings, missing @, missing /, trailing dots)
2. **Expected:** All return descriptive errors, none panic.

## Failure Signals

- Any subtest in `go test ./tool/... -run TestFQN` failing
- `go vet ./tool/...` reporting issues

## Not Proven By This UAT

- How downstream slices (S04-S12) consume these types at integration boundaries
- Runtime behavior — these are pure types with no I/O

## Notes for Tester

Tests run in ~3ms total. No external dependencies required.



## Tasks
- [x] **T01: Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.** — 
