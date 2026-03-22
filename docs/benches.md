# Benchmarks

Benchmarks for the full tool invocation pipeline.

Run with: `go test ./invoke/... -bench=. -benchmem -benchtime=5s -count=3 -run='^$'`

## Results

### VFS round-trip (wasix-cli, Wasmer)

```
goos: linux
goarch: amd64
cpu: AMD Ryzen AI 9 HX 370 w/ Radeon 890M

BenchmarkVFSRoundTrip/VFSServerOnly-18      ~92000      ~65μs/op
BenchmarkVFSRoundTrip/WASMGuestCold-18         ~52     ~120ms/op
BenchmarkVFSRoundTrip/WASMGuestCached-18      ~543      ~11ms/op
BenchmarkVFSRoundTrip/FullRoundTrip-18        ~343      ~17ms/op
```

### Cross-runtime comparison

Measured on AMD Ryzen AI 9 HX 370, Linux 6.19, 2026-03-21:

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkWasixCLIVFSRoundTrip` | 135,572,645 | 7,218,645 | 10,858 |
| `BenchmarkWasip2CLIExec` | 341,059,587 | 12,269,442 | 18,925 |
| `BenchmarkWasip2CLIVFSRoundTrip` | 25,384,063 | 4,667,871 | 6,719 |

Notes:
- `Wasip2CLIExec` includes a real HTTP call to httpbin.org so network latency dominates.
- `Wasip2CLIVFSRoundTrip` is ~5x faster than `WasixCLIVFSRoundTrip` because the wasip2 component is smaller and Wasmtime compilation caching is enabled.

## What each benchmark measures

| Benchmark | Scope | What it isolates |
|---|---|---|
| **VFSServerOnly** | UDS round-trip: open, write, seek, read, close | VFS server + msgpack framing overhead |
| **WASMGuestCold** | VFS server + wasixcli-sandbox (cache cleared) | Cranelift compilation + process startup + ProxyFs I/O |
| **WASMGuestCached** | VFS server + wasixcli-sandbox (warm cache) | Process startup + module deserialization + ProxyFs I/O |
| **FullRoundTrip** | Complete `invoke.RunWithVFS()` with all caches warm | TS check (session cached) + esbuild + QuickJS + cached WASM guest |
| **WasixCLIVFSRoundTrip** | wasix-cli full invoke stack | TS → exec → WASM → ProxyFs → MemFS |
| **Wasip2CLIExec** | wasip2-cli HTTP tool | TS → exec → WASM (real network call) |
| **Wasip2CLIVFSRoundTrip** | wasip2-cli full invoke stack | TS → exec → WASM → staging sync → MemFS |

## Per-layer breakdown (FullRoundTrip)

| Layer | Cold | Warm | Notes |
|---|---|---|---|
| TypeScript type-check | ~30ms | ~0.5ms | Session cached per package via `DidChangeFile` |
| esbuild bundle | ~0.4ms | ~0.4ms | TS → JS bundling |
| QuickJS eval | ~2ms | ~2ms | JS execution in QuickJS |
| WASM guest | ~120ms | ~11ms | Compiled module cached in `$TMPDIR` |
| VFS server | ~0.07ms | ~0.07ms | UDS msgpack round-trips |
| **Total** | **~154ms** | **~17ms** | **9x improvement** |

## Caching strategies

### WASM module caching

Compiled WASM modules are cached in `$TMPDIR/toolbox-wasm-cache/` using the
xxhash of the input `.wasm` bytes as the key. On cache hit, `Module::deserialize()`
loads pre-compiled native code instead of running Cranelift (120ms → 11ms).

Wasmtime (wasip2-cli) uses its built-in file-based compilation cache via
`Cache::new(CacheConfig::new())`.

### TypeScript check session caching

A global session cache in `invoke` maps `*tool.Package` to a reusable LSP
`CheckSession`. On first invocation for a package the session is created with
all tool source files. On subsequent invocations, only the generated runner
file (containing the args) is updated via `DidChangeFile` — the compiler
reuses parsed ASTs and type info for all unchanged source files (30ms → 0.5ms).

## Prerequisites

Benchmarks require built artifacts and will skip if missing:

```bash
cargo build --manifest-path wasmcli-sandbox/Cargo.toml
```
