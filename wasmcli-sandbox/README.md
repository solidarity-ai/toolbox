# wasmcli-sandbox

`wasmcli-sandbox` is the Rust host binary for WASM sandbox runtimes.

It supports two modes via `--runtime`:
- `wasix-cli` — runs WASIX modules via Wasmer (existing behavior)
- `wasip2-cli` — runs WASI P2 components via Wasmtime (with wasi:http support)

Usage: `wasmcli-sandbox --runtime wasix-cli <wasm-path> [args...]`

The current end-to-end smoke path is exercised through `runtime/tswasmcli`.

## Toolchain

This host is currently pinned to `rust = "1.88.0"` in
[.mise.toml](/home/mackross/dev/toolbox/.mise.toml) at the repo root.

Newer Rust toolchains on this machine hit a Wasmer linker failure around
`__rust_probestack`; pinning the host toolchain is the current clean fix.
