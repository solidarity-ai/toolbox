# Benchmarks

Go benchmarks exercise the full tool invocation path for each WASM runtime.

## Running

```bash
# All benchmarks
go test ./invoke/ -bench=. -benchmem -count=1 -timeout 300s

# Specific runtime
go test ./invoke/ -bench=BenchmarkWasixCLI -benchmem -count=1
go test ./invoke/ -bench=BenchmarkWasip2CLI -benchmem -count=1
```

## Available benchmarks

| Benchmark | Runtime | What it exercises |
|---|---|---|
| `BenchmarkWasixCLIVFSRoundTrip` | wasix-cli (Wasmer) | Full VFS round-trip: TS → exec → WASM → ProxyFs → MemFS |
| `BenchmarkWasip2CLIExec` | wasip2-cli (Wasmtime) | HTTP tool execution: TS → exec → WASM (network call) |
| `BenchmarkWasip2CLIVFSRoundTrip` | wasip2-cli (Wasmtime) | Full VFS round-trip: TS → exec → WASM → ProxyFs → MemFS |

## Prerequisites

Benchmarks require built artifacts and will skip if missing:

```bash
# Build the sandbox binary
cargo build --manifest-path wasmcli-sandbox/Cargo.toml

# WASM fixtures are pre-compiled and checked into the repo
```
