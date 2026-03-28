---
sliceId: S03
uatType: artifact-driven
verdict: PASS
date: 2026-03-27T15:44:27Z
---

# UAT Result — S03

## Checks

| Check | Mode | Result | Notes |
|-------|------|--------|-------|
| Smoke test: `go test ./tool/... -v -count=1 -run TestFQN` | artifact | PASS | All 31 subtests pass in 0.004s |
| 1. ModulePath parsing accepts valid host/path | artifact | PASS | `valid_basic` and `valid_nested` subtests pass |
| 2. ModulePath rejects single-segment or no-dot paths | artifact | PASS | `single_segment`, `host_missing_dot` subtests pass |
| 3. Version parsing accepts semver with prerelease and build | artifact | PASS | `valid_semver_prerelease_build` subtest passes |
| 4. Pseudo-version parsing and decomposition | artifact | PASS | `valid_pseudo` subtest passes |
| 5. Malformed pseudo-versions rejected | artifact | PASS | `bad_pseudo_timestamp`, `bad_pseudo_commit_length`, `bad_pseudo_commit_charset` subtests all pass |
| 6. ToolFQN round-trip | artifact | PASS | `TestFQNParseToolFQN/valid_semver` and `valid_pseudo` pass |
| 7. PackageVer round-trip | artifact | PASS | `TestFQNParsePackageVer/valid_semver` and `valid_pseudo` pass |
| Edge cases: empty and malformed inputs | artifact | PASS | Empty, missing-at, missing-slash, leading/trailing dot subtests all pass with no panics |
| `go vet ./tool/...` clean | artifact | PASS | No findings |

## Overall Verdict

PASS — All 31 subtests across 5 test functions pass; go vet reports no issues.

## Notes

No additional context needed. Tests complete in 4ms with no external dependencies.
