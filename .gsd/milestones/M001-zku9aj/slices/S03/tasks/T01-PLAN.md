---
estimated_steps: 24
estimated_files: 2
skills_used: []
---

# T01: Implement FQN types, parsing, and table-driven tests

Add all FQN identity types and their Parse/String/validation functions to the tool package, plus comprehensive table-driven tests.

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

## Inputs

- `tool/tool.go`

## Expected Output

- `tool/fqn.go`
- `tool/fqn_test.go`

## Verification

go test ./tool/... -v -count=1 -run TestFQN && go vet ./tool/...
