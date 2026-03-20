# wasixcli

`wasixcli` is the Rust runtime for the typescript+wasix-sandbox path.

Phase 1 now runs one real WASI guest `.wasm`:
- the host takes a wasm path plus guest argv
- executes the guest through Wasmer
- forwards guest `stdout` and `stderr`

This is still intentionally narrow:
- no general Toolbox asset loading yet
- no shared VFS mounting yet
- no real Google Workspace CLI guest yet

The current end-to-end smoke path is exercised through `runtime/tswasixcli`.

## Toolchain

This host is currently pinned to `rust = "1.88.0"` in
[.mise.toml](/home/mackross/dev/toolbox/.mise.toml) at the repo root.

Newer Rust toolchains on this machine hit a Wasmer linker failure around
`__rust_probestack`; pinning the host toolchain is the current clean fix.
