use anyhow::{Context, Result};
use std::env;
use wasmtime::{Cache, CacheConfig, Config, Engine, Store};
use wasmtime::component::{Component, Linker, ResourceTable};
use wasmtime_wasi::p2::{IoView, WasiCtx, WasiCtxBuilder, WasiView, pipe::MemoryOutputPipe};
use wasmtime_wasi::{DirPerms, FilePerms};
use wasmtime_wasi_http::{WasiHttpCtx, WasiHttpView};

use crate::proxy_fs;

struct WasmState {
    wasi: WasiCtx,
    http: WasiHttpCtx,
    table: ResourceTable,
}

impl IoView for WasmState {
    fn table(&mut self) -> &mut ResourceTable {
        &mut self.table
    }
}

impl WasiView for WasmState {
    fn ctx(&mut self) -> &mut WasiCtx {
        &mut self.wasi
    }
}

impl WasiHttpView for WasmState {
    fn ctx(&mut self) -> &mut WasiHttpCtx {
        &mut self.http
    }
}

pub fn run(wasm_bytes: &[u8], guest_args: &[String]) -> Result<()> {
    let mut config = Config::new();
    config.wasm_component_model(true);
    config.async_support(false);

    // Enable compilation caching so repeated runs skip recompilation.
    if let Ok(cache) = Cache::new(CacheConfig::new()) {
        config.cache(Some(cache));
    }

    let engine = Engine::new(&config).context("create wasmtime engine")?;
    let component = Component::new(&engine, wasm_bytes).context("compile wasm component")?;

    let stdout_pipe = MemoryOutputPipe::new(65536);
    let stderr_pipe = MemoryOutputPipe::new(65536);

    let mut wasi_builder = WasiCtxBuilder::new();
    wasi_builder
        .inherit_env()
        .inherit_network()
        .allow_ip_name_lookup(true)
        .stdout(stdout_pipe.clone())
        .stderr(stderr_pipe.clone())
        .args(guest_args);

    // Mount the shared VFS if a socket path is provided.
    // Connect to the VFS proxy and mount as /work via preopened_dir,
    // using a dedicated connection for filesystem operations.
    if let Ok(socket_path) = env::var("TOOLBOX_VFS_SOCK") {
        let proxy = proxy_fs::ProxyFs::connect(&socket_path)
            .context("connect to VFS proxy")?;
        // Create a host-side directory backed by the VFS proxy.
        // We use preopened_dir with /dev/shm for in-memory backing.
        let work_dir = std::path::PathBuf::from(format!("/dev/shm/toolbox-vfs-{}", std::process::id()));
        std::fs::create_dir_all(&work_dir).context("create /dev/shm work dir")?;
        // Sync files from VFS proxy into the in-memory directory.
        sync_from_proxy(&proxy.conn, &work_dir).context("sync from VFS proxy")?;
        wasi_builder.preopened_dir(
            &work_dir,
            "/work",
            DirPerms::all(),
            FilePerms::all(),
        ).context("preopen /work")?;
    }

    let state = WasmState {
        wasi: wasi_builder.build(),
        http: WasiHttpCtx::new(),
        table: ResourceTable::new(),
    };

    let mut store = Store::new(&engine, state);

    let mut linker = Linker::new(&engine);
    wasmtime_wasi::p2::add_to_linker_sync(&mut linker).context("link wasi")?;
    wasmtime_wasi_http::add_only_http_to_linker_sync(&mut linker).context("link wasi-http")?;

    let command =
        wasmtime_wasi::p2::bindings::sync::Command::instantiate(&mut store, &component, &linker)
            .context("instantiate wasi command component")?;

    let run_result = command.wasi_cli_run().call_run(&mut store);

    // Sync files back to VFS proxy after execution.
    if let Ok(socket_path) = env::var("TOOLBOX_VFS_SOCK") {
        let proxy = proxy_fs::ProxyFs::connect(&socket_path)
            .context("reconnect to VFS proxy for sync-back")?;
        let work_dir = std::path::PathBuf::from(format!("/dev/shm/toolbox-vfs-{}", std::process::id()));
        sync_to_proxy(&proxy.conn, &work_dir).context("sync to VFS proxy")?;
        let _ = std::fs::remove_dir_all(&work_dir);
    }

    let stdout = stdout_pipe.contents();
    let stderr = stderr_pipe.contents();

    print!("{}", String::from_utf8_lossy(&stdout));
    eprint!("{}", String::from_utf8_lossy(&stderr));

    match run_result {
        Ok(Ok(())) => Ok(()),
        Ok(Err(())) => {
            std::process::exit(1);
        }
        Err(err) => Err(err).context("run wasm component"),
    }
}

/// Download all files from VFS proxy into a host directory.
fn sync_from_proxy(conn: &proxy_fs::Conn, host_dir: &std::path::Path) -> Result<()> {
    use std::path::Path;
    let resp = conn.raw_call(&proxy_fs::WireRequest::path_op(proxy_fs::OP_READ_DIR, Path::new("/")))
        .context("read VFS root")?;
    for entry in &resp.entries {
        if entry.meta.is_file {
            let file_resp = conn.raw_call(&{
                let mut req = proxy_fs::WireRequest {
                    op: proxy_fs::OP_OPEN,
                    path: Some(format!("/{}", entry.name)),
                    to_path: None,
                    open_opts: Some(proxy_fs::WireOpenOpts {
                        read: true,
                        write: false,
                        create: false,
                        create_new: false,
                        append: false,
                        truncate: false,
                    }),
                    handle: 0,
                    data: None,
                    len: 0,
                    seek_from: 0,
                    seek_pos: 0,
                };
                req
            }).context("open VFS file")?;

            let mut data = Vec::new();
            loop {
                let mut read_req = proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_READ, file_resp.handle);
                read_req.len = 65536;
                let read_resp = conn.raw_call(&read_req).context("read VFS file")?;
                if read_resp.data.is_empty() {
                    break;
                }
                data.extend_from_slice(&read_resp.data);
                if (read_resp.n as usize) < 65536 {
                    break;
                }
            }
            let _ = conn.raw_call(&proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_CLOSE, file_resp.handle));

            std::fs::write(host_dir.join(&entry.name), &data)
                .with_context(|| format!("write {}", entry.name))?;
        }
    }
    Ok(())
}

/// Upload all files from a host directory back to the VFS proxy.
fn sync_to_proxy(conn: &proxy_fs::Conn, host_dir: &std::path::Path) -> Result<()> {
    for entry in std::fs::read_dir(host_dir).context("read host dir")? {
        let entry = entry?;
        if entry.file_type()?.is_file() {
            let name = entry.file_name().to_string_lossy().into_owned();
            let data = std::fs::read(entry.path())?;

            let open_resp = conn.raw_call(&proxy_fs::WireRequest {
                op: proxy_fs::OP_OPEN,
                path: Some(format!("/{name}")),
                to_path: None,
                open_opts: Some(proxy_fs::WireOpenOpts {
                    read: false,
                    write: true,
                    create: true,
                    create_new: false,
                    append: false,
                    truncate: true,
                }),
                handle: 0,
                data: None,
                len: 0,
                seek_from: 0,
                seek_pos: 0,
            }).context("open VFS file for write")?;

            let mut req = proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_WRITE, open_resp.handle);
            req.data = Some(data);
            conn.raw_call(&req).context("write VFS file")?;

            let _ = conn.raw_call(&proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_CLOSE, open_resp.handle));
        }
    }
    Ok(())
}
