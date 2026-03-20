# Benchmarks

Benchmarks for the full tool invocation pipeline:
Go → TypeScript (QuickJS) → WASM (Wasmer) → ProxyFs → VFS server.

Run with: `go test ./invoke/... -bench=BenchmarkVFSRoundTrip -benchtime=5s -count=3`

## Results

```
goos: linux
goarch: amd64
cpu: AMD Ryzen AI 9 HX 370 w/ Radeon 890M

BenchmarkVFSRoundTrip/VFSServerOnly-18      ~92000      ~65μs/op
BenchmarkVFSRoundTrip/WASMGuestCold-18         ~52     ~120ms/op
BenchmarkVFSRoundTrip/WASMGuestCached-18      ~543      ~11ms/op
BenchmarkVFSRoundTrip/FullRoundTrip-18        ~343      ~17ms/op
```

## What each benchmark measures

| Benchmark | Scope | What it isolates |
|---|---|---|
| **VFSServerOnly** | UDS round-trip: open, write, seek, read, close | VFS server + msgpack framing overhead |
| **WASMGuestCold** | VFS server + wasixcli-sandbox (cache cleared) | Cranelift compilation + process startup + ProxyFs I/O |
| **WASMGuestCached** | VFS server + wasixcli-sandbox (warm cache) | Process startup + module deserialization + ProxyFs I/O |
| **FullRoundTrip** | Complete `invoke.RunWithVFS()` with all caches warm | TS check (session cached) + esbuild + QuickJS + cached WASM guest |

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

### TypeScript check session caching

A global session cache in `invoke` maps `*tool.Package` to a reusable LSP
`CheckSession`. On first invocation for a package the session is created with
all tool source files. On subsequent invocations, only the generated runner
file (containing the args) is updated via `DidChangeFile` — the compiler
reuses parsed ASTs and type info for all unchanged source files (30ms → 0.5ms).
