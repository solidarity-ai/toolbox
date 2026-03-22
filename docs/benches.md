# Benchmarks

Benchmarks for the full tool invocation pipeline.

Run with: `go test ./invoke/... -bench=. -benchmem -benchtime=5s -count=3 -run='^$'`

## Results

Measured on AMD Ryzen AI 9 HX 370, Linux 6.19, 2026-03-22.

### Summary

| Benchmark | ms/op | ops/sec | B/op | allocs/op |
|---|---:|---:|---:|---:|
| VFSServerOnly | 0.07 | 15,262 | 5,333 | 102 |
| WASMGuestCold (wasix) | 124 | 8.1 | 37,380 | 252 |
| WASMGuestCached (wasix) | 117 | 8.5 | 36,523 | 251 |
| FullRoundTrip (wasix) | 131 | 7.6 | 7,457,679 | 10,910 |
| **Wasip2CLIVFSRoundTrip** | **27** | **37.3** | **4,939,251** | **6,755** |

### Raw output

```
goos: linux
goarch: amd64
cpu: AMD Ryzen AI 9 HX 370 w/ Radeon 890M

BenchmarkVFSRoundTrip/VFSServerOnly-18         18508       65535 ns/op      5333 B/op      102 allocs/op
BenchmarkVFSRoundTrip/WASMGuestCold-18             9   124083781 ns/op     37380 B/op      252 allocs/op
BenchmarkVFSRoundTrip/WASMGuestCached-18           9   117142131 ns/op     36523 B/op      251 allocs/op
BenchmarkVFSRoundTrip/FullRoundTrip-18             8   131016445 ns/op   7457679 B/op    10910 allocs/op
BenchmarkWasip2CLIVFSRoundTrip-18                 43    26803046 ns/op   4939251 B/op     6755 allocs/op
```

### Key takeaways

- **Wasip2 VFS is ~5x faster** than wasix full round-trip (27ms vs 131ms) due to smaller component and Wasmtime's compilation cache.
- **VFS server overhead is negligible** at 0.07ms — the bottleneck is WASM compilation/startup.
- **Wasix cold vs cached** shows only marginal improvement (124ms → 117ms) because module deserialization is nearly as expensive as compilation on this platform.

## What each benchmark measures

| Benchmark | Scope | What it isolates |
|---|---|---|
| **VFSServerOnly** | UDS round-trip: open, write, seek, read, close | VFS server + msgpack framing overhead |
| **WASMGuestCold** | VFS server + sandbox (cache cleared) | Cranelift compilation + process startup + ProxyFs I/O |
| **WASMGuestCached** | VFS server + sandbox (warm cache) | Process startup + module deserialization + ProxyFs I/O |
| **FullRoundTrip** | Complete `invoke.RunWithVFS()` with all caches warm | TS check + esbuild + QuickJS + cached WASM guest |
| **Wasip2CLIVFSRoundTrip** | wasip2-cli full invoke stack | TS → exec → WASM → staging sync → MemFS |

## Per-layer breakdown (FullRoundTrip, wasix-cli)

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
