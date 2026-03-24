# SecureExec Evaluation: Could It Replace QuickJS in Toolbox?

**Date**: 2026-03-24
**Status**: Research complete
**Verdict**: **Not a fit.** SecureExec solves a different problem and cannot replace our current stack.

---

## Executive Summary

SecureExec (secureexec.dev) is a Node.js library for sandboxed JavaScript execution using V8 isolates. It targets a fundamentally different architecture than toolbox: it requires a Node.js (or Bun) host process, has no Go SDK, no WASM execution support, and cannot be embedded into a Go binary. Adopting it would require rewriting the entire toolbox runtime layer in TypeScript/JavaScript and abandoning our Go-native architecture.

---

## 1. What is SecureExec?

**Product**: A TypeScript/JavaScript npm package (`secure-exec`) for executing untrusted code safely.
**Creator**: Rivet (rivet.dev)
**Repository**: [github.com/rivet-dev/secure-exec](https://github.com/rivet-dev/secure-exec)
**License**: Apache-2.0
**Language composition**: TypeScript (85%), Rust (12%), C (1%), JavaScript (1%)

**Runtime**: Native V8 isolates — the same isolation primitive behind Cloudflare Workers and browser tab isolation. Each execution gets its own heap, globals, and deny-by-default permission boundary.

**Sandbox model**: Process-level isolation. Sandboxed code runs in a dedicated child process with a separate V8 isolate. The host communicates with the sandbox over a Unix domain socket using length-prefixed MessagePack framing, authenticated with a one-time 128-bit token.

**Three trust boundaries**:
1. **Process boundary**: Separate OS process with isolated memory, FDs, and signal handlers
2. **Runtime boundary**: V8 isolate with bridge functions serializing requests over IPC
3. **Host boundary**: Your application code (trusted infrastructure)

**Key design choices**:
- Deny-by-default permissions for filesystem, network, child processes, env vars
- Configurable CPU time limits (`cpuTimeLimitMs`) and memory caps (`memoryLimit`)
- Timing hardening: high-resolution timers frozen by default to mitigate side channels
- ~3.4 MB memory per execution instance
- ~17.9 ms cold start (claimed)

**Source**: secureexec.dev, secureexec.dev/docs/security-model, github.com/rivet-dev/secure-exec

---

## 2. JS/TS Execution

**How SecureExec handles it**: Code is executed via `NodeRuntime.exec()` or `NodeRuntime.run()`. Full Node.js API compatibility is claimed — core modules (`fs`, `http`, `child_process`, `dns`, `process`, `os`) are bridged to host capabilities. TypeScript support is available via an optional `@secure-exec/typescript` package that provides source-level type checking and compilation.

**Our current pipeline**:
```
.ts files → typescript-go (type checking, cached per-package)
         → esbuild (TS→JS transpilation, in-memory FS plugin)
         → QuickJS (execution with injected host functions)
```

**Comparison**:

| Dimension | Toolbox (current) | SecureExec |
|---|---|---|
| JS engine | QuickJS (embedded in Go via CGo) | V8 (separate child process) |
| TS compilation | esbuild (Go-native) | Optional `@secure-exec/typescript` package |
| Type checking | typescript-go (Go-native, cached) | Optional package, unclear caching |
| Embedding | Compiles into single Go binary | Requires Node.js/Bun host process |
| Module resolution | Custom memFS plugin, no bare imports | Full npm compatibility, `node_modules` bridging |
| ES module support | ES2023 target, ESM format | Full Node.js module system |
| Performance | QuickJS: interpreter, fast startup | V8: JIT-compiled, ~17.9ms cold start |

**Key gap**: SecureExec requires a Node.js or Bun host process. Our toolbox compiles to a single Go binary with QuickJS embedded via CGo. There is no way to embed SecureExec into Go.

---

## 3. Networking / Fetch

**SecureExec**: Network access is deny-by-default, granted via permission callbacks. HTTP works through bridged Node.js `http` module. Custom permission functions can filter by hostname. The networking layer bridges to "real host capabilities, not stubbed."

**Our architecture**: We use a MITM proxy (`transport/mitmproxy/`) that:
- Listens on localhost with a dynamic port
- Generates per-host TLS certificates signed by an embedded CA
- Intercepts HTTPS traffic via CONNECT tunneling
- Provides an `Observer` interface for audit/logging
- Is injected via `HTTPS_PROXY` and `SSL_CERT_FILE` environment variables

**Could we route SecureExec through our MITM proxy?**

Theoretically yes — if SecureExec respects `HTTPS_PROXY` env vars (which it blocks by default under deny-by-default env access). But the integration would be indirect: we'd need to run a Node.js process, configure it to proxy through our Go MITM server, and manage the CA certificate injection. This is significantly more complex than our current approach where the proxy is embedded in the same Go process.

**Verdict**: Our current approach is simpler and more tightly integrated. SecureExec's permission-based network model is useful but doesn't replace our audit/interception layer.

---

## 4. Filesystem / VFS

**SecureExec**: Offers `createInMemoryFileSystem()` and filesystem permission callbacks. The sandbox gets a virtual filesystem with deny-by-default access. Node.js `fs` module operations are bridged through the permission layer. A read-only dependency overlay at `/app/node_modules` is available for npm packages.

**Our VFS architecture**:
- Go-side VFS server (`vfs/server.go`) on a Unix domain socket
- MemFS (`vfs/memfs.go`) as backing store
- Wire protocol: 4-byte BE length prefix + msgpack
- Operations: ReadDir, CreateDir, RemoveDir, Rename, Open, Read, Write, Seek, etc.
- Rust-side proxy (`wasmcli-sandbox/src/proxy_fs.rs`) implements `wasmer_wasix::virtual_fs::FileSystem`
- Mounted at `/work` in WASM guests

**Could SecureExec replace our VFS?**

No. Our VFS serves a specific purpose: providing filesystem access to WASM guests running in Wasmer/Wasmtime. SecureExec's in-memory filesystem is designed for Node.js code, not for cross-runtime WASM guests communicating over Unix sockets. Even if we used SecureExec for the JS layer, we'd still need our VFS for WASM tool execution.

**Verdict**: No replacement possible. Different abstraction levels and different consumers.

---

## 5. Host Functions / Extensibility

**SecureExec**: Extensibility is through "system drivers" — pluggable components that provide host capabilities. Two built-in drivers: `createNodeDriver()` (native FS + network) and `createBrowserDriver()` (OPFS + fetch). Permission callbacks control what sandboxed code can access. The MCP "Code Mode" integration shows tool dispatch via `codemode.toolName()` proxied to host implementations.

**Our host function model**:
```go
type Host struct {
    Exec      func(binary string, args []string) (ExecResult, error)
    ReadFile  func(path string) (string, error)
    WriteFile func(path string, data string) error
}
```
- Injected via `qjs.FuncToJS()` converting Go functions to JS callables
- Set as `globalThis.__toolboxExec`, `__toolboxReadFile`, `__toolboxWriteFile`
- Shim JS wraps them as clean APIs: `exec()`, `fs.readFileSync()`, `fs.writeFileSync()`
- `codemode` adds `__invokeTool` for tool-to-tool calls

**Comparison**: Our model is direct Go→JS function binding. SecureExec's model is IPC-mediated driver callbacks. Both support custom host functions, but:

- **Ours**: Zero-copy, synchronous, in-process. Go functions called directly from JS.
- **SecureExec**: Cross-process IPC, serialized, async. Host functions mediated by the driver layer.

**Verdict**: Our approach is more efficient for short-lived tool executions. SecureExec's approach trades latency for stronger isolation.

---

## 6. WASM Support

**SecureExec**: **No WASM execution support.** Not mentioned anywhere in the documentation or repository. SecureExec executes JavaScript/TypeScript only.

**Our WASM stack**:
- **WASIX mode**: Wasmer v6.1.0 with Cranelift JIT, module caching via xxhash
- **WASIP2 mode**: Wasmtime v33 with Component Model, automatic compilation caching, wasi-http support
- Both connect to our VFS over Unix sockets
- Invoked from TS via `exec("binary-name", args)` host function

**Verdict**: Complete non-starter for our WASM use case. SecureExec cannot replace Wasmer or Wasmtime. Even if we used SecureExec for JS execution, we'd still need our entire WASM runtime stack.

---

## 7. Performance

**SecureExec benchmarks** (from secureexec.dev):
- Cold start: ~17.9 ms (p50 ~16.2 ms)
- Memory per instance: ~3.4 MB
- Claimed 176x faster than "sandboxes" (likely container-based)

**Our QuickJS characteristics**:
- QuickJS is an interpreter (no JIT), but has very fast startup
- Embedded in-process — no IPC overhead, no child process spawn
- For short-lived tool executions (our primary use case), startup time dominates
- esbuild bundling is extremely fast (Go-native)

**Analysis**: SecureExec's 17.9ms cold start includes spawning a child process, establishing IPC, and initializing V8. QuickJS in-process initialization is likely faster for our use case because:

1. No process spawn overhead
2. No IPC channel setup
3. No authentication handshake
4. QuickJS runtime creation is lightweight (~1ms range)

For long-running computations, V8's JIT would dominate QuickJS's interpreter. But our tools are short-lived — they parse args, call a few host functions (exec, readFile, writeFile), and return a result. The JS execution itself is minimal; the real work happens in Go host functions or WASM binaries.

**Verdict**: QuickJS is likely faster for our workload pattern. V8 JIT benefits don't materialize in sub-100ms tool executions.

---

## 8. Go Integration

**SecureExec**: **No Go SDK.** The library is a pure npm package (`npm install secure-exec`). It's written in TypeScript (85%) and Rust (12%). It runs on Node.js or Bun. There are no Go bindings, no CGo bridge, no way to embed it in a Go process.

**Our current Go integration**:
- QuickJS via `github.com/fastschema/qjs` — CGo bindings, embedded in Go binary
- esbuild via `github.com/evanw/esbuild` — pure Go, compiled in
- typescript-go — Go-native TypeScript checker
- All three compile into a single Go binary with zero external runtime dependencies

**To use SecureExec from Go**, we would need to:
1. Ship Node.js alongside our Go binary (or require it as a dependency)
2. Shell out to `node` to run SecureExec
3. Communicate via stdin/stdout or sockets
4. Parse results back in Go
5. Manage the Node.js process lifecycle

This would negate every architectural advantage of our current design.

**Verdict**: Fundamental architecture mismatch. SecureExec requires a JS runtime host; we are a Go binary.

---

## 9. Licensing and Maturity

| Dimension | SecureExec | Our current stack |
|---|---|---|
| License | Apache-2.0 | QuickJS: MIT; esbuild: MIT; typescript-go: Apache-2.0 |
| Open source | Yes | Yes |
| Maturity | Relatively new (Rivet ecosystem) | QuickJS: battle-tested since 2019; esbuild: widely adopted |
| Production use | Targets AI agent platforms | QuickJS used in embedded systems, IoT, CLI tools |
| GitHub stars | Modest (new project) | QuickJS: 8k+; esbuild: 38k+ |
| Maintenance | Active (Rivet team) | QuickJS-Go bindings: smaller community; esbuild: well-maintained |

**Verdict**: Both stacks have acceptable licensing. Our current stack is more mature and battle-tested. SecureExec is newer and less proven in production.

---

## 10. Migration Cost Analysis

### What we'd gain

1. **Stronger JS sandbox isolation** — V8 process isolation is more robust than QuickJS in-process execution. If a V8 bug causes a crash, it stays in the child process.
2. **Full Node.js API compatibility** — npm packages would work out of the box, including native `fetch`, `fs`, `http`.
3. **Built-in permission model** — Deny-by-default with composable permission functions.
4. **Code Mode / MCP integration** — SecureExec has purpose-built support for AI agent tool chaining.

### What we'd lose

1. **Single-binary deployment** — We'd need Node.js as a runtime dependency.
2. **Go-native architecture** — The entire runtime layer would need to be TypeScript.
3. **In-process efficiency** — Every tool execution would incur process spawn + IPC overhead.
4. **WASM execution** — SecureExec doesn't support WASM. We'd keep Wasmer/Wasmtime.
5. **VFS integration** — SecureExec can't serve as a VFS host for WASM guests.
6. **MITM proxy integration** — Would need indirect proxy configuration instead of embedded.
7. **Type checking pipeline** — typescript-go's cached checking is deeply integrated.

### What we'd keep regardless

Even with SecureExec as the JS runtime, we'd still need:
- Wasmer + Wasmtime for WASM execution
- VFS over Unix sockets for WASM filesystem access
- MITM proxy for HTTP interception
- Go binary as the main orchestrator
- Some form of TS→JS compilation

### Migration effort

- **Rewrite**: `runtime/quickts/`, `codemode/`, `invoke/`, `fsoverlay/` — the entire JS execution pipeline
- **New dependency**: Node.js/Bun runtime required on host
- **New IPC layer**: Go↔Node.js communication for every tool execution
- **Testing**: All tool execution tests would need rewriting
- **Packaging**: Binary distribution model would fundamentally change

**Estimated scope**: Major rewrite (weeks to months), not an incremental migration.

---

## Recommendation

**Do not adopt SecureExec.** The mismatch is architectural, not feature-level:

1. **SecureExec is a Node.js library.** We are a Go binary. There is no Go SDK and no way to embed SecureExec in Go without shipping Node.js as a dependency.

2. **SecureExec doesn't do WASM.** Half our runtime stack (Wasmer, Wasmtime, VFS, wasmcli-sandbox) would remain unchanged.

3. **Our workload doesn't benefit from V8.** Short-lived tool executions with minimal JS computation don't benefit from JIT compilation. QuickJS's fast in-process startup is a better fit.

4. **The isolation trade-off isn't worth it.** V8 process isolation is stronger than QuickJS in-process execution, but our tools already run in a controlled environment with limited host function access. The security boundary that matters most is our host function layer (what `exec`, `readFile`, `writeFile` are allowed to do), not the JS engine's isolation.

### If stronger JS isolation is needed

Consider these alternatives instead:

- **Harden the QuickJS sandbox**: Audit host function injection, add resource limits (execution time, memory), validate all inputs at the Go boundary.
- **Run QuickJS in a WASM sandbox**: Compile QuickJS to WASM and run it in Wasmtime/Wasmer for process-level isolation without requiring Node.js.
- **Use V8 via Go bindings**: Projects like [nicholasgasior/gopher-v8](https://github.com/nicholasgasior/gopher-v8) or [nicholasgasior/goja](https://github.com/nicholasgasior/goja) (a Go V8 alternative) could provide V8-level performance if JS execution speed becomes a bottleneck, though they add CGo complexity.

---

## Sources

- [secureexec.dev](https://secureexec.dev) — Product page, benchmarks, feature overview
- [secureexec.dev/docs/security-model](https://secureexec.dev/docs/security-model) — Security architecture, trust boundaries, IPC details
- [secureexec.dev/docs/sdk-overview](https://secureexec.dev/docs/sdk-overview) — SDK API, drivers, permissions
- [secureexec.dev/docs/use-cases/code-mode](https://secureexec.dev/docs/use-cases/code-mode) — MCP Code Mode integration
- [github.com/rivet-dev/secure-exec](https://github.com/rivet-dev/secure-exec) — Source code, license (Apache-2.0)
- Codebase analysis of `runtime/quickts/`, `vfs/`, `transport/mitmproxy/`, `wasmcli-sandbox/`, `invoke/`, `codemode/`
