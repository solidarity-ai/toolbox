# Knowledge

- In `.gsd` toolbox worktrees, `go test` can resolve package paths through the linked `/home/mackross/dev/toolbox` checkout instead of the active worktree. When that happens, run tests with `GOWORK=$(pwd)/go.work go test ...` so Go uses the current worktree's files. (The simpler `GOWORK=` override is sufficient; `env -u PWD GOMOD=` is not needed.)
- For pseudo-version fixture and resolver tests, derive the `v0.0.0-<timestamp>-<sha>` timestamp from `git show --format=%cI` and normalize it to UTC. Handwritten timestamps drift from actual commit metadata and cause false failures once timestamp validation is enforced.
