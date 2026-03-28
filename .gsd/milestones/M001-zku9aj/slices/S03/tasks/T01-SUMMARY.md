---
id: T01
parent: S03
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: ["tool/fqn.go", "tool/fqn_test.go", ".gsd/milestones/M001-zku9aj/slices/S03/tasks/T01-SUMMARY.md"]
key_decisions: ["Treat digit-prefixed `v0.0.0-...` versions as pseudo-version candidates that must match the exact Go pseudo-version shape rather than falling through to generic semver prerelease parsing."]
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Formatted the new files with gofmt and ran `go test ./tool/... -v -count=1 -run TestFQN` and `go vet ./tool/...`; both passed."
completed_at: 2026-03-28T00:32:40.719Z
blocker_discovered: false
---

# T01: Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.

> Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.

## What Happened
---
id: T01
parent: S03
milestone: M001-zku9aj
key_files:
  - tool/fqn.go
  - tool/fqn_test.go
  - .gsd/milestones/M001-zku9aj/slices/S03/tasks/T01-SUMMARY.md
key_decisions:
  - Treat digit-prefixed `v0.0.0-...` versions as pseudo-version candidates that must match the exact Go pseudo-version shape rather than falling through to generic semver prerelease parsing.
duration: ""
verification_result: passed
completed_at: 2026-03-28T00:32:40.720Z
blocker_discovered: false
---

# T01: Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.

**Added typed FQN parsers and round-trip tests for module, version, package, and tool identities.**

## What Happened

Implemented tool/fqn.go with ModulePath, Version, ToolPath, ToolFQN, and PackageVer types plus Parse and String helpers. Validation now enforces host/path module paths, dot-separated tool paths, semver support with prerelease/build metadata, and exact Go pseudo-version parsing with timestamp/commit extraction helpers. Added tool/fqn_test.go with table-driven coverage for valid and invalid parse cases, round-trip fidelity, pseudo-version helper extraction, and required edge cases. During verification, tightened ParseVersion so malformed pseudo-like strings are rejected instead of being accepted as generic semver prereleases.

## Verification

Formatted the new files with gofmt and ran `go test ./tool/... -v -count=1 -run TestFQN` and `go vet ./tool/...`; both passed.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `go test ./tool/... -v -count=1 -run TestFQN` | 0 | ✅ pass | 142ms |
| 2 | `go vet ./tool/...` | 0 | ✅ pass | 63ms |


## Deviations

None.

## Known Issues

None.

## Files Created/Modified

- `tool/fqn.go`
- `tool/fqn_test.go`
- `.gsd/milestones/M001-zku9aj/slices/S03/tasks/T01-SUMMARY.md`


## Deviations
None.

## Known Issues
None.
