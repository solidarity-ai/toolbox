# Knowledge

- In `.gsd` toolbox worktrees, `go test` can resolve package paths through the linked `/home/mackross/dev/toolbox` checkout instead of the active worktree. When that happens, run tests with `env -u PWD GOWORK=$(pwd)/go.work GOMOD=$(pwd)/go.mod go test ...` so Go uses the current worktree's files.
