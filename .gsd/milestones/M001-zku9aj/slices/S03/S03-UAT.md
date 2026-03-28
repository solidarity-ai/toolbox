# S03: FQN types and parsing — UAT

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
