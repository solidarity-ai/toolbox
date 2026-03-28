# S03: FQN types and parsing

**Goal:** Provide typed FQN identity types (ModulePath, Version, ToolPath, ToolFQN, PackageVer) with parsing, formatting, validation, and round-trip faithfulness. Pseudo-versions are recognized and decomposable.
**Demo:** After this: # S03: FQN types and parsing — UAT

**Milestone:** M001-zku9aj
**Written:** 2026-03-27T15:45:08.070Z

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
- [x] **T01: Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.** — Add all FQN identity types and their Parse/String/validation functions to the tool package, plus comprehensive table-driven tests.

## Why
R001 requires parseable, formattable, round-trip-faithful FQN types. Every downstream slice (S04–S12) depends on these types.

## Steps
1. Create `tool/fqn.go` with these types (all as named string types except ToolFQN/PackageVer which are structs):
   - `ModulePath` — validated: ≥2 path segments, first segment has a dot
   - `Version` — validated: semver `v{major}.{minor}.{patch}` with optional pre-release/build metadata, OR pseudo-version `v0.0.0-{14digits}-{12hex}`
   - `ToolPath` — validated: dot-separated non-empty segments
   - `ToolFQN` struct `{Module ModulePath, Version Version, Tool ToolPath}` — parsed from `module@version/toolpath`
   - `PackageVer` struct `{Module ModulePath, Version Version}` — parsed from `module@version`
2. Implement `ParseModulePath(s string) (ModulePath, error)`, `ParseVersion(s string) (Version, error)`, `ParseToolPath(s string) (ToolPath, error)`, `ParseToolFQN(s string) (ToolFQN, error)`, `ParsePackageVer(s string) (PackageVer, error)`
3. Implement `String()` methods on all types
4. Implement `Version.IsPseudo() bool`, `Version.PseudoTimestamp() string`, `Version.PseudoCommit() string` helpers
5. Create `tool/fqn_test.go` with table-driven tests:
   - Valid and invalid cases for each Parse function
   - Round-trip: `Parse*(x.String()) == x` for all valid inputs
   - Pseudo-version helper extraction
   - Edge cases: empty strings, missing @, missing /, no dot in host, single-segment paths

## Constraints
- Use standard library only (`strings`, `regexp`, `fmt`, `errors`)
- Types live in `tool` package per D002
- Pseudo-version format must exactly match Go modules: `v0.0.0-{yyyyMMddHHmmss}-{12hex}`
- ModulePath validation: first segment must contain a dot (hostname), at least host/path
- Version regex should accept optional pre-release (`-alpha.1`) and build metadata (`+build`) on semver
  - Estimate: 45m
  - Files: tool/fqn.go, tool/fqn_test.go
  - Verify: go test ./tool/... -v -count=1 -run TestFQN && go vet ./tool/...
