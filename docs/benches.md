# Benchmarks

Benchmarks for the full tool invocation pipeline.

Run with: `go test ./invoke/... -bench=. -benchmem -benchtime=5s -count=3 -run='^$'`

## Results

Measured on AMD Ryzen AI 9 HX 370, Linux 6.19, 2026-03-22.

### Summary

| Benchmark | ms/op | ops/sec | B/op | allocs/op |
|---|---:|---:|---:|---:|
| VFSServerOnly | 0.07 | 14,926 | 5,335 | 102 |
| WASMGuestCold (wasix) | 119 | 8.4 | 37,346 | 252 |
| WASMGuestCached (wasix) | 14 | 73.3 | 36,827 | 251 |
| FullRoundTrip (wasix) | 21 | 47.5 | 4,823,552 | 6,695 |
| Wasip2CLIVFSRoundTrip | 26 | 38.3 | 4,936,272 | 6,755 |

### Raw output

```
goos: linux
goarch: amd64
cpu: AMD Ryzen AI 9 HX 370 w/ Radeon 890M

BenchmarkVFSRoundTrip/VFSServerOnly-18         17439       67029 ns/op      5335 B/op      102 allocs/op
BenchmarkVFSRoundTrip/WASMGuestCold-18             9   118741145 ns/op     37346 B/op      252 allocs/op
BenchmarkVFSRoundTrip/WASMGuestCached-18          84    13639727 ns/op     36827 B/op      251 allocs/op
BenchmarkVFSRoundTrip/FullRoundTrip-18            49    21068685 ns/op   4823552 B/op     6695 allocs/op
BenchmarkWasip2CLIVFSRoundTrip-18                 43    26108097 ns/op   4936272 B/op     6755 allocs/op
```

### Key takeaways

- **Wasix caching gives ~9x speedup**: cold 119ms → cached 14ms (Cranelift compilation → Module::deserialize).
- **Wasix and wasip2 are comparable when cached**: wasix 21ms vs wasip2 26ms for full VFS round-trip.
- **VFS server overhead is negligible** at 0.07ms — the bottleneck is WASM compilation/startup.
- **Cold compilation dominates**: 119ms for wasix (Cranelift), both runtimes are sub-30ms once cached.

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
