# Benchmarks

Go benchmarks exercise the full tool invocation path for each WASM runtime.

## Running

```bash
# All benchmarks
go test ./invoke/ -bench=. -benchmem -count=1 -timeout 300s -run='^$'

# Specific runtime
go test ./invoke/ -bench=BenchmarkWasixCLI -benchmem -count=1 -run='^$'
go test ./invoke/ -bench=BenchmarkWasip2CLI -benchmem -count=1 -run='^$'
```

## Results

Measured on AMD Ryzen AI 9 HX 370, Linux 6.19, 2026-03-21:

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkWasixCLIVFSRoundTrip` | 135,572,645 | 7,218,645 | 10,858 |
| `BenchmarkWasip2CLIExec` | 341,059,587 | 12,269,442 | 18,925 |
| `BenchmarkWasip2CLIVFSRoundTrip` | 25,384,063 | 4,667,871 | 6,719 |

Notes:
- `Wasip2CLIExec` includes a real HTTP call to httpbin.org so network latency dominates.
- `Wasip2CLIVFSRoundTrip` is faster than `WasixCLIVFSRoundTrip` because the wasip2 component is smaller and Wasmtime compilation caching is enabled.

## Available benchmarks

| Benchmark | Runtime | What it exercises |
|---|---|---|
| `BenchmarkWasixCLIVFSRoundTrip` | wasix-cli (Wasmer) | Full VFS round-trip: TS → exec → WASM → ProxyFs → MemFS |
| `BenchmarkWasip2CLIExec` | wasip2-cli (Wasmtime) | HTTP tool execution: TS → exec → WASM (network call) |
| `BenchmarkWasip2CLIVFSRoundTrip` | wasip2-cli (Wasmtime) | Full VFS round-trip: TS → exec → WASM → /dev/shm sync → MemFS |

## Prerequisites

Benchmarks require built artifacts and will skip if missing:

```bash
# Build the sandbox binary
cargo build --manifest-path wasmcli-sandbox/Cargo.toml

# WASM fixtures are pre-compiled and checked into the repo
```
