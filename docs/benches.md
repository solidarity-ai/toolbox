# Benchmarks

Benchmarks for the full tool invocation pipeline:
Go → TypeScript (QuickJS) → WASM (Wasmer) → ProxyFs → VFS server.

Run with: `go test ./invoke/... -bench=BenchmarkVFSRoundTrip -benchtime=5s -count=3`

## Results

```
goos: linux
goarch: amd64
cpu: AMD Ryzen AI 9 HX 370 w/ Radeon 890M

BenchmarkVFSRoundTrip/VFSServerOnly-18      ~90000      ~67μs/op
BenchmarkVFSRoundTrip/WASMGuestCold-18         ~50     ~120ms/op
BenchmarkVFSRoundTrip/WASMGuestCached-18      ~540      ~11ms/op
BenchmarkVFSRoundTrip/FullRoundTrip-18        ~126      ~46ms/op
```

## What each benchmark measures

| Benchmark | Scope | What it isolates |
|---|---|---|
| **VFSServerOnly** | UDS round-trip: open, write, seek, read, close | VFS server + msgpack framing overhead |
| **WASMGuestCold** | VFS server + wasixcli-sandbox (cache cleared) | Cranelift compilation + process startup + ProxyFs I/O |
| **WASMGuestCached** | VFS server + wasixcli-sandbox (warm cache) | Process startup + module deserialization + ProxyFs I/O |
| **FullRoundTrip** | Complete `invoke.RunWithVFS()` with warm cache | TS type-check + esbuild + QuickJS + cached WASM guest |

## Per-layer breakdown (FullRoundTrip)

| Layer | Cost | Notes |
|---|---|---|
| TypeScript type-check (`toolbox.Check`) | ~30ms | Compiler parses all files + semantic analysis |
| esbuild bundle | ~0.4ms | TS → JS bundling |
| QuickJS eval | ~2ms | JS execution in QuickJS |
| WASM guest (cached) | ~11ms | Process spawn + deserialized module + ProxyFs I/O |
| VFS server | ~0.07ms | UDS msgpack round-trips |

TypeScript type-checking dominates at ~65% of the full roundtrip. This validates
args against the tool's TypeScript types on every invocation. The cost is in
`compiler.NewProgram` + `GetSemanticDiagnostics` — the actual TypeScript compiler,
not infrastructure overhead.

## WASM module caching

Compiled WASM modules are cached in `$TMPDIR/toolbox-wasm-cache/` using the
xxhash of the input `.wasm` bytes as the key. On cache hit, `Module::deserialize()`
loads pre-compiled native code instead of running Cranelift. This gives ~11x
speedup on the WASM layer (120ms → 11ms).
