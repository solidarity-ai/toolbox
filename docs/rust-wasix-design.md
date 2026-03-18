# Rust WASIX Host: Architecture & Design

## Overview

The Rust binary (`toolbox-wasm-host`) is a thin, stable WASM execution engine. It loads a WASM module, configures a WASIX sandbox (filesystem, networking, environment, resource limits), executes it, and reports the result.

**All intelligence lives in Go.** The Rust binary has no knowledge of tools, packages, credentials, scoping, or permissions. It runs what Go tells it to run, with the sandbox configuration Go provides.

## Why Rust + Wasmer

- **Wasmer** has the strongest WASIX support — threading, networking, filesystem, subprocess spawning
- **WASIX** is a superset of WASI that adds real networking (sockets), pthreads, process forking, and more — enabling existing Go/Rust CLIs compiled to WASM to work without modification
- **The Rust binary is small** (~500-1000 lines) — it's a configured Wasmer runner that should stay approachable to build and maintain without deep Rust specialization
- **Process boundary with Go** provides crash isolation — a WASM crash can't take down the Go service

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Go Service                                                       │
│                                                                   │
│  ┌──────────────┐                                                │
│  │ invoke        │ ← orchestrates one tool execution             │
│  └──────┬───────┘                                                │
│         │                                                        │
│         │ 1. Prepares virtual filesystem:                        │
│         │    - /etc/ssl/certs/ca-certificates.crt (MITM CA)     │
│         │    - environment vars (HTTPS_PROXY, tool config)       │
│         │    - /tool/input.json (tool input data)                │
│         │    - /credentials/ (provisioned secrets)               │
│         │                                                        │
│         │ 2. Starts MITM proxy on localhost:PORT (if proxy mode) │
│         │                                                        │
│  ┌──────▼────────┐         ┌─────────────────────┐              │
│  │ stdin/stdout   │         │ MITM proxy :PORT     │              │
│  │ to wasm-host   │         │                      │              │
│  │                │         │ - TLS termination     │              │
│  │ → {exec json}  │         │   (custom CA)        │              │
│  │ ← {result json}│         │ - Credential replace  │              │
│  │                │         │ - URL scope enforce   │              │
│  │                │         │ - Full req/res logging │              │
│  │                │         │ - Rate limiting       │              │
│  └───────┬────────┘         └──────────▲────────────┘              │
│          │                             │                          │
├──────────┼─── process boundary ────────┼──────────────────────────┤
│  Rust    │                             │                          │
│  ┌───────▼──────────┐                  │                          │
│  │ toolbox-wasm-host │                  │                          │
│  │                   │    WASIX sockets │                          │
│  │ 1. Read stdin     │    (via proxy)   │                          │
│  │ 2. Load WASM      │                  │                          │
│  │ 3. Configure WASIX│                  │                          │
│  │    - filesystem ──┼── mounted from Go's prepared dirs          │
│  │    - networking ──┼── enabled, filtered by allowedHosts        │
│  │    - env vars  ───┼── HTTPS_PROXY + tool config                │
│  │ 4. Execute        │                  │                          │
│  │ 5. Write stdout   │                  │                          │
│  └───────────────────┘                  │                          │
│          │                              │                          │
│  ┌───────▼──────────────────────────────┘──────────┐              │
│  │ WASM module (compiled Go/Rust/C binary)          │              │
│  │                                                  │              │
│  │  Tool code: http.Get("https://api.slack.com/..") │              │
│  │    → runtime reads HTTPS_PROXY env var           │              │
│  │    → CONNECT api.slack.com:443 → localhost:PORT  │              │
│  │    → TLS handshake trusts custom CA from /etc/ssl│              │
│  │    → Go MITM proxy sees full HTTP traffic        │              │
│  │    → Proxy replaces Authorization header         │              │
│  │    → Proxy forwards to real api.slack.com        │              │
│  │    → Response flows back through proxy           │              │
│  │    → Tool gets response, continues execution     │              │
│  └──────────────────────────────────────────────────┘              │
└────────────────────────────────────────────────────────────────────┘
```

## Stdin/Stdout Protocol

The protocol is **unidirectional**. Go sends one JSON message on stdin, Rust sends one JSON message on stdout. No callbacks, no bidirectional messaging, no message correlation.

### Input (Go → Rust, JSON on stdin)

```json
{
  "module": "/path/to/module.wasm",
  "entrypoint": "_start",
  "args": ["list-users", "--domain", "acme.com"],
  "env": {
    "HTTPS_PROXY": "http://proxy.toolbox.internal:9451",
    "GOOGLE_WORKSPACE_DOMAIN": "acme.com",
    "SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt"
  },
  "stdin": "",
  "filesystem": [
    {
      "host": "/tmp/exec-abc/ca.pem",
      "guest": "/etc/ssl/certs/ca-certificates.crt"
    },
    {
      "host": "/tmp/exec-abc/hosts",
      "guest": "/etc/hosts"
    },
    {
      "host": "/tmp/exec-abc/input.json",
      "guest": "/tool/input.json"
    },
    {
      "host": "/tmp/exec-abc/creds",
      "guest": "/credentials",
      "writable": true
    }
  ],
  "network": {
    "enabled": true,
    "allowedHosts": ["*.googleapis.com", "oauth2.googleapis.com", "127.0.0.1"]
  },
  "limits": {
    "timeoutSeconds": 60,
    "memoryMB": 256,
    "maxOpenFiles": 32
  }
}
```

### Output (Rust → Go, JSON on stdout)

```json
{
  "exitCode": 0,
  "stdout": "...",
  "stderr": "...",
  "durationMs": 1823,
  "peakMemoryMB": 47,
  "error": null
}
```

### Error Output (Rust → Go, JSON on stdout)

```json
{
  "exitCode": -1,
  "stdout": "",
  "stderr": "",
  "durationMs": 12,
  "peakMemoryMB": 0,
  "error": {
    "type": "module_load_failed",
    "message": "Failed to compile WASM module: invalid magic bytes"
  }
}
```

Error types:
- `module_load_failed` — WASM module couldn't be loaded or compiled
- `execution_timeout` — exceeded `limits.timeoutSeconds`
- `memory_exceeded` — exceeded `limits.memoryMB`
- `execution_error` — WASM trapped or panicked
- `config_error` — invalid input JSON or unsupported configuration

### Field Reference

| Field | Type | Required | Rust Action |
|---|---|---|---|
| `module` | string (path) | yes | `wasmer::Module::from_file()` — load and compile the WASM module |
| `entrypoint` | string | no (default: `_start`) | Which exported function to call. `_start` is the WASIX standard. |
| `args` | string[] | no | Passed as WASIX command-line arguments (`argv`) |
| `env` | map<string, string> | no | Set as WASIX environment variables (visible to `environ_get`) |
| `stdin` | string | no | Piped to the WASM module's stdin file descriptor |
| `filesystem` | array | no | Each entry mounts a host path into the WASM virtual filesystem |
| `filesystem[].host` | string | yes | Absolute path on the real host filesystem |
| `filesystem[].guest` | string | yes | Path inside the WASM virtual filesystem |
| `filesystem[].writable` | boolean | no (default: false) | Mount read-only by default, writable if true |
| `network.enabled` | boolean | yes | Enable or disable WASIX socket syscalls entirely |
| `network.allowedHosts` | string[] | no | Wasmer network filter rules — restrict which hosts the module can connect to |
| `limits.timeoutSeconds` | number | no (default: 30) | Spawn a watchdog thread, kill WASM execution after timeout |
| `limits.memoryMB` | number | no (default: 256) | Set WASM linear memory limit |
| `limits.maxOpenFiles` | number | no (default: 32) | Limit the number of open file descriptors |

## Network Modes

Go controls the network mode entirely through the input JSON. The Rust binary doesn't know about "modes" — it just applies what it's told.

### Proxy Mode

For tools where Go wants full HTTP visibility (credential replacement, request/response logging, URL-level scope enforcement).

Go sets up:
- MITM proxy on `localhost:PORT` with a per-execution custom CA
- `HTTPS_PROXY` env var pointing to the proxy
- Custom CA cert mounted at `/etc/ssl/certs/ca-certificates.crt`
- `SSL_CERT_FILE` env var pointing to the CA cert (some runtimes check this)
- `allowedHosts` includes `127.0.0.1` (the proxy) plus the tool's declared hosts

**How it works for HTTPS:**
1. WASM module's HTTP client reads `HTTPS_PROXY` env var (Go `net/http`, Rust `reqwest`, and most HTTP clients do this by default)
2. Client sends `CONNECT api.slack.com:443` to the proxy
3. Go MITM proxy connects to real `api.slack.com`, gets real TLS cert
4. Proxy generates a fake cert for `api.slack.com` signed by the custom CA
5. WASM module's TLS stack trusts the custom CA (mounted in virtual filesystem)
6. Proxy now sees all HTTP traffic in the clear — can log requests/responses, replace credential headers, enforce URL allowlists
7. Proxy forwards to real destination, pipes response back

**Proxy hostname resolution:** Go's `net/http` bypasses proxies set to localhost/127.0.0.1. To work around this, the proxy address uses a virtual hostname `proxy.toolbox.internal` which resolves to `127.0.0.1` via a custom `/etc/hosts` mounted in the WASM filesystem:

```
# /etc/hosts mounted into WASM
127.0.0.1  proxy.toolbox.internal
```

The env var is set to `HTTPS_PROXY=http://proxy.toolbox.internal:PORT`. Go's HTTP client treats this as a non-local address, uses the proxy, and WASIX resolves the hostname to loopback via `/etc/hosts`.

**Compatibility:** Go's `net/http` and Rust's `reqwest` both respect proxy env vars and system CA stores by default. This covers the majority of tools compiled to WASM.

### Open Mode

For tools that pin certificates, use non-HTTP protocols, manage their own credentials, or where HTTP-level interception isn't needed.

Go sets up:
- No MITM proxy
- No `HTTPS_PROXY` env var
- No custom CA
- `allowedHosts` from the tool's package manifest (defense-in-depth)

The WASM module makes direct network connections through WASIX sockets. Wasmer's network filter restricts which hosts are reachable. Go gets connection-level visibility only (which hosts were contacted, timing, success/fail) — not HTTP request/response content.

### No Network

For tools that don't need network access at all (pure computation, file processing).

```json
{ "network": { "enabled": false } }
```

WASIX socket syscalls are disabled. Any network call from the WASM module fails immediately.

## Filesystem Mounting

The Rust binary mounts host directories/files into the WASM virtual filesystem using wasmer's `WasiFs` API.

**Key design decisions:**

- **Read-only by default.** Every mount is read-only unless `writable: true` is explicitly set. This prevents tools from modifying host files accidentally.
- **Go prepares everything.** The Rust binary doesn't create files or directories — Go prepares a temp directory with everything the WASM module needs, and tells Rust where to mount it.
- **Writable mounts for persistent volumes.** When a tool declares it needs a persistent volume (e.g. OAuth token cache), Go creates the volume directory and mounts it writable. The directory persists between executions.

**Typical filesystem layout inside WASM:**

```
/etc/ssl/certs/ca-certificates.crt  ← custom CA (proxy mode) or system CAs
/etc/hosts                           ← maps proxy.toolbox.internal → 127.0.0.1
/tool/input.json                     ← tool input data
/tool/config.json                    ← tool-specific config
/credentials/                        ← provisioned secrets (writable if tool needs to cache tokens)
/tmp/                                ← writable temp directory
```

## Resource Limits

### Timeout

The Rust binary spawns a watchdog thread when execution starts. If the WASM module hasn't completed after `timeoutSeconds`, the watchdog kills the execution and returns:

```json
{
  "exitCode": -1,
  "error": { "type": "execution_timeout", "message": "Execution exceeded 60s timeout" }
}
```

### Memory

WASM linear memory is bounded by `memoryMB`. If the module tries to grow memory beyond this limit, the `memory.grow` instruction returns -1 (failure), and the module typically panics or exits.

### File Descriptors

`maxOpenFiles` limits how many files the WASM module can have open simultaneously. Prevents resource exhaustion from runaway tools.

## Rust Binary Internals

### Responsibilities

1. Read JSON from stdin, parse into config struct
2. Load WASM module from `config.module` path
3. Configure WASIX environment:
   - Mount filesystem entries
   - Set environment variables
   - Set command-line arguments
   - Configure network (enable/disable sockets, set host filters)
   - Set resource limits (memory, file descriptors)
4. Pipe `config.stdin` to WASM stdin if provided
5. Start watchdog timer for timeout
6. Execute the module
7. Capture stdout, stderr, exit code, peak memory usage
8. Write result JSON to stdout

### What Rust Does NOT Do

- No credential management
- No HTTP proxying
- No tool metadata interpretation
- No scope or permission evaluation
- No package management or registry interaction
- No logging beyond stdout/stderr capture
- No knowledge of "tools", "packages", or "toolsets"
- No bidirectional communication with Go during execution

### Approximate Structure

```rust
// Pseudocode — actual implementation will use wasmer + wasmer_wasix crates

fn main() {
    // 1. Read config from stdin
    let config: ExecConfig = serde_json::from_reader(io::stdin()).unwrap();
    
    // 2. Load WASM module
    let store = Store::default();
    let module = Module::from_file(&store, &config.module)?;
    
    // 3. Configure WASIX
    let mut wasi_env = WasiState::new(&config.entrypoint)
        .args(&config.args)
        .envs(&config.env)
        .finalize()?;
    
    // Mount filesystem
    for mount in &config.filesystem {
        if mount.writable {
            wasi_env.fs.mount_host_path(&mount.guest, &mount.host, true)?;
        } else {
            wasi_env.fs.mount_host_path(&mount.guest, &mount.host, false)?;
        }
    }
    
    // Configure networking
    if config.network.enabled {
        wasi_env.enable_networking();
        for host in &config.network.allowed_hosts {
            wasi_env.add_network_filter(host)?;
        }
    }
    
    // Set memory limit
    // Set file descriptor limit
    
    // 4. Execute with timeout
    let start = Instant::now();
    let result = with_timeout(config.limits.timeout_seconds, || {
        wasi_env.run(module)
    });
    
    // 5. Collect output
    let output = ExecResult {
        exit_code: result.exit_code(),
        stdout: wasi_env.read_stdout(),
        stderr: wasi_env.read_stderr(),
        duration_ms: start.elapsed().as_millis(),
        peak_memory_mb: wasi_env.peak_memory() / (1024 * 1024),
        error: result.err().map(|e| ExecError::from(e)),
    };
    
    // 6. Write result to stdout
    serde_json::to_writer(io::stdout(), &output).unwrap();
}
```

### Dependencies

| Crate | Purpose |
|---|---|
| `wasmer` | WASM runtime — compiles and executes WASM modules |
| `wasmer-wasix` | WASIX support — provides POSIX-like syscalls (networking, threads, filesystem) |
| `serde` + `serde_json` | JSON serialization for the stdin/stdout protocol |
| `glob` / `regex` | Host allowlist pattern matching (e.g. `*.googleapis.com`) |

### Build & Distribution

The Rust binary compiles to a single static binary. No runtime dependencies.

```bash
cargo build --release --target x86_64-unknown-linux-musl
# Produces: target/x86_64-unknown-linux-musl/release/toolbox-wasm-host
```

The Go service embeds or ships this binary alongside itself. On startup, Go verifies the binary exists and is the correct version.

## Wasmer-Specific Details

### Network Filtering

Wasmer's `--net` flag with WASIX supports fine-grained network filters:

```
ipv4:allow=127.0.0.1:80/in
```

From the wasmer CLI docs:
> Enable networking with the host network. Allows WASI modules to open TCP and UDP connections, create sockets, ... Optionally, a set of network filters could be defined which allows fine-grained control over the network sandbox.

The Rust binary translates `allowedHosts` patterns from the input JSON into wasmer network filter rules.

### WASIX Capabilities Used

| WASIX Syscall | Used For |
|---|---|
| `environ_get` / `environ_sizes_get` | Reading environment variables (HTTPS_PROXY, SSL_CERT_FILE, tool config) |
| `fd_read` / `fd_write` | Stdin/stdout/stderr and file I/O |
| `path_open` / `fd_prestat_get` | Filesystem access (mounted from host) |
| `sock_open` / `sock_connect` / `sock_send` / `sock_recv` | Network connections (HTTP calls through proxy or direct) |
| `thread_spawn` / `thread_join` | Multi-threading (for tools that use goroutines, Tokio, etc.) |
| `clock_time_get` | Wall clock and monotonic time |
| `proc_exit` | Clean exit with exit code |
| `random_get` | Cryptographic randomness (needed for TLS) |

### WASIX Capabilities NOT Enabled

| WASIX Syscall | Why Disabled |
|---|---|
| `proc_fork` / `proc_exec` / `proc_spawn` | Subprocess spawning — security risk. May enable in future with additional sandboxing. |
| `port_*` | Network interface management — not needed and dangerous. |
| `tty_get` / `tty_set` | TTY access — tools run non-interactively. |

## Edge Cases & Known Limitations

### Tools That Don't Respect HTTP_PROXY

Some tools use custom HTTP clients that bypass proxy environment variables. In proxy mode:

- **Mitigation:** `allowedHosts` is set to include `127.0.0.1` (the proxy). If a tool ignores the proxy and tries to connect directly, wasmer's network filter should block the connection to any host not on the allowlist.
- **Caveat:** Need to verify that wasmer network filters can enforce this correctly — if the filter applies to connection establishment but the tool has already resolved DNS, it might connect to an IP that doesn't match the hostname pattern. Needs testing.

### Certificate Pinning

Some tools embed their own CA certificates and don't use the system trust store. In proxy mode:

- **Impact:** TLS handshake fails because the tool doesn't trust the MITM CA
- **Fallback:** These tools must use "open" network mode. Go gets connection-level visibility only.
- **Detection:** Tool will fail with a TLS error in proxy mode. The error message in stderr typically makes this obvious.

### Non-HTTP Protocols

gRPC, WebSocket, raw TCP — the MITM proxy handles HTTP/HTTPS. Other protocols:

- **gRPC over HTTP/2:** Should work through the MITM proxy since it's HTTP underneath
- **WebSocket:** The initial HTTP upgrade goes through the proxy. After upgrade, the proxy must forward raw TCP frames — standard for MITM proxies.
- **Raw TCP/UDP:** Not intercepted by the HTTP proxy. Only controlled via wasmer network filters (host allowlist). Phase 2 if needed.

### WASM Module Compilation Time

First load of a WASM module requires compilation (can take seconds for large modules). Wasmer supports caching compiled modules:

- **Optimization:** Go can pass a cache directory. Rust checks for a cached compiled version before compiling from scratch.
- **Protocol extension:** Add optional `cacheDir` field to the input JSON. Rust uses `wasmer::Module::deserialize_from_file()` for cached modules.

### Large stdout/stderr

If a tool produces megabytes of output, it all gets buffered and returned in the result JSON.

- **Mitigation:** Add optional `maxStdoutBytes` / `maxStderrBytes` limits. Rust truncates and sets a `truncated: true` flag in the result.
- **Alternative:** Stream stdout/stderr to files, return file paths in the result.
