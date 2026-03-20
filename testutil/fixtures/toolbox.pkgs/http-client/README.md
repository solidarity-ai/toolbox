# http-client test fixture

A Go WASM binary that makes a vanilla HTTPS GET request, compiled to a
wasip2 component. Used to test HTTP proxying through the MITM transport layer
with a wasmtime-based sandbox runtime.

## Source

`wasm-src/main.go` — a plain `http.Get` with no proxy awareness. Proxy
routing is injected by the runtime via `HTTPS_PROXY` and `SSL_CERT_FILE`
environment variables.

## How to recompile

Requires tinygo (0.40.x), Go 1.25, and wasm-tools:

```bash
mise install tinygo@0.40.1 go@1.25 "aqua:bytecodealliance/wasm-tools"

cd wasm-src

GOWORK=off \
GOTOOLCHAIN=local \
GOROOT=$(mise where go@1.25) \
PATH=$(mise where go@1.25)/bin:$(mise where tinygo@0.40.1)/tinygo/bin:$PATH \
  tinygo build -target=wasip2 -o ../dist/http-client.wasm .
```

The `GOTOOLCHAIN=local` flag prevents Go from auto-upgrading past 1.25,
which is the latest version tinygo 0.40.x supports.

## Runtime

This fixture expects `ts+wasip2-sandbox` — a wasmtime-based runtime that
supports wasip2 components. This runtime does not exist yet.
