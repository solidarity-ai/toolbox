---
id: T03
parent: S01
milestone: M001-zku9aj
provides: []
requires: []
affects: []
key_files: [".github/workflows/ci.yml", ".gsd/milestones/M001-zku9aj/slices/S01/tasks/T03-SUMMARY.md"]
key_decisions: []
patterns_established: []
drill_down_paths: []
observability_surfaces: []
duration: ""
verification_result: "Confirmed `setup-node` and the emulate install step are present in `.github/workflows/ci.yml`. The task-plan Python YAML validation command failed locally because PyYAML is not installed, so I validated the file with Ruby's YAML loader instead. Ran `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/... -v -count=1 -timeout 60s`, which passed and exercised both `registry/testutil/emulatetest` and `registry/testutil/gitfixture`. Ran the slice-level verification `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s`, which passed and showed emulate starting on a port, HTTP calls succeeding, and release metadata being verified."
completed_at: 2026-03-26T22:41:58.747Z
blocker_discovered: false
---

# T03: Updated CI to install Node.js 22 and pre-install emulate so the registry integration suite runs in GitHub Actions.

> Updated CI to install Node.js 22 and pre-install emulate so the registry integration suite runs in GitHub Actions.

## What Happened
---
id: T03
parent: S01
milestone: M001-zku9aj
key_files:
  - .github/workflows/ci.yml
  - .gsd/milestones/M001-zku9aj/slices/S01/tasks/T03-SUMMARY.md
key_decisions:
  - (none)
duration: ""
verification_result: mixed
completed_at: 2026-03-26T22:41:58.753Z
blocker_discovered: false
---

# T03: Updated CI to install Node.js 22 and pre-install emulate so the registry integration suite runs in GitHub Actions.

**Updated CI to install Node.js 22 and pre-install emulate so the registry integration suite runs in GitHub Actions.**

## What Happened

Updated `.github/workflows/ci.yml` by inserting `actions/setup-node@v4` with `node-version: '22'` immediately after checkout and adding an `Install emulate` step that runs `npm install -g emulate`. All existing Go and Rust setup, build, lint, vet, and test steps were preserved unchanged. Verified the workflow from the explicit `/home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj` path because the shell tool's default cwd differs from the user-facing worktree path in this auto-mode run.

## Verification

Confirmed `setup-node` and the emulate install step are present in `.github/workflows/ci.yml`. The task-plan Python YAML validation command failed locally because PyYAML is not installed, so I validated the file with Ruby's YAML loader instead. Ran `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/... -v -count=1 -timeout 60s`, which passed and exercised both `registry/testutil/emulatetest` and `registry/testutil/gitfixture`. Ran the slice-level verification `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s`, which passed and showed emulate starting on a port, HTTP calls succeeding, and release metadata being verified.

## Verification Evidence

| # | Command | Exit Code | Verdict | Duration |
|---|---------|-----------|---------|----------|
| 1 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo 'YAML valid'` | 1 | ❌ fail | 20000ms |
| 2 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && ruby -e "require 'yaml'; YAML.load_file('.github/workflows/ci.yml'); puts 'YAML valid'"` | 0 | ✅ pass | 17100ms |
| 3 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'setup-node' .github/workflows/ci.yml && echo 'setup-node present'` | 0 | ✅ pass | 14300ms |
| 4 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && grep -q 'npm install -g emulate' .github/workflows/ci.yml && echo 'emulate install present'` | 0 | ✅ pass | 11600ms |
| 5 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/... -v -count=1 -timeout 60s` | 0 | ✅ pass | 8800ms |
| 6 | `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && go test ./registry/testutil/emulatetest/ -v -count=1 -timeout 60s` | 0 | ✅ pass | 5900ms |


## Deviations

The task plan specified `python3 -c "import yaml; ..."` for YAML validation, but the local environment does not have PyYAML installed. I used Ruby's built-in YAML loader as an equivalent parser-based validation instead.

## Known Issues

None. For this auto-mode run, shell-based verification commands need an explicit `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && ...` prefix because the shell tool defaults to a different worktree root than the user-facing path.

## Files Created/Modified

- `.github/workflows/ci.yml`
- `.gsd/milestones/M001-zku9aj/slices/S01/tasks/T03-SUMMARY.md`


## Deviations
The task plan specified `python3 -c "import yaml; ..."` for YAML validation, but the local environment does not have PyYAML installed. I used Ruby's built-in YAML loader as an equivalent parser-based validation instead.

## Known Issues
None. For this auto-mode run, shell-based verification commands need an explicit `cd /home/mackross/dev/toolbox/.gsd/worktrees/M001-zku9aj && ...` prefix because the shell tool defaults to a different worktree root than the user-facing path.
