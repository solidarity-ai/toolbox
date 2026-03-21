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
    // Single connection is reused for both sync-from and sync-back to
    // guarantee ordered operations over the UDS.
    let vfs = if let Ok(socket_path) = env::var("TOOLBOX_VFS_SOCK") {
        let proxy = proxy_fs::ProxyFs::connect(&socket_path)
            .context("connect to VFS proxy")?;
        let work_dir = std::path::PathBuf::from(format!("/dev/shm/toolbox-vfs-{}", std::process::id()));
        std::fs::create_dir_all(&work_dir).context("create /dev/shm work dir")?;
        sync_from_proxy(&proxy.conn, &work_dir).context("sync from VFS proxy")?;
        wasi_builder.preopened_dir(
            &work_dir,
            "/work",
            DirPerms::all(),
            FilePerms::all(),
        ).context("preopen /work")?;
        Some((proxy, work_dir))
    } else {
        None
    };

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

    // Sync files back using the same connection.
    if let Some((proxy, work_dir)) = &vfs {
        sync_to_proxy(&proxy.conn, work_dir).context("sync to VFS proxy")?;
        let _ = std::fs::remove_dir_all(work_dir);
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

/// Download all files from VFS proxy into a host directory (recursive).
fn sync_from_proxy(conn: &proxy_fs::Conn, host_dir: &std::path::Path) -> Result<()> {
    sync_from_proxy_dir(conn, "/", host_dir)
}

fn sync_from_proxy_dir(conn: &proxy_fs::Conn, vfs_dir: &str, host_dir: &std::path::Path) -> Result<()> {
    use std::path::Path;
    let resp = conn.raw_call(&proxy_fs::WireRequest::path_op(proxy_fs::OP_READ_DIR, Path::new(vfs_dir)))
        .with_context(|| format!("read VFS dir {vfs_dir}"))?;
    for entry in &resp.entries {
        let vfs_path = if vfs_dir == "/" {
            format!("/{}", entry.name)
        } else {
            format!("{}/{}", vfs_dir, entry.name)
        };

        if entry.meta.is_dir {
            let sub_host_dir = host_dir.join(&entry.name);
            std::fs::create_dir_all(&sub_host_dir)
                .with_context(|| format!("create dir {}", entry.name))?;
            sync_from_proxy_dir(conn, &vfs_path, &sub_host_dir)?;
        } else if entry.meta.is_file {
            let file_resp = conn.raw_call(&proxy_fs::WireRequest {
                op: proxy_fs::OP_OPEN,
                path: Some(vfs_path.clone()),
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
            }).with_context(|| format!("open VFS file {vfs_path}"))?;

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

/// Upload all files from a host directory back to the VFS proxy (recursive).
#[allow(dead_code)]
fn sync_to_proxy(conn: &proxy_fs::Conn, host_dir: &std::path::Path) -> Result<()> {
    sync_to_proxy_dir(conn, host_dir, "/")
}

fn sync_to_proxy_dir(conn: &proxy_fs::Conn, host_dir: &std::path::Path, vfs_prefix: &str) -> Result<()> {
    for entry in std::fs::read_dir(host_dir).context("read host dir")? {
        let entry = entry?;
        let name = entry.file_name().to_string_lossy().into_owned();
        let vfs_path = if vfs_prefix == "/" {
            format!("/{name}")
        } else {
            format!("{vfs_prefix}/{name}")
        };

        if entry.file_type()?.is_dir() {
            // Create directory on proxy, then recurse.
            let _ = conn.raw_call(&proxy_fs::WireRequest::path_op(
                proxy_fs::OP_CREATE_DIR,
                std::path::Path::new(&vfs_path),
            ));
            sync_to_proxy_dir(conn, &entry.path(), &vfs_path)?;
        } else if entry.file_type()?.is_file() {
            let data = std::fs::read(entry.path())?;

            let open_resp = conn.raw_call(&proxy_fs::WireRequest {
                op: proxy_fs::OP_OPEN,
                path: Some(vfs_path.clone()),
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
            }).with_context(|| format!("open VFS file {vfs_path} for write"))?;

            let mut req = proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_WRITE, open_resp.handle);
            req.data = Some(data);
            conn.raw_call(&req).context("write VFS file")?;

            let _ = conn.raw_call(&proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_CLOSE, open_resp.handle));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_mock_vfs::MockVfsServer;

    // ── sync_from_proxy tests ──────────────────────────────────────────

    #[test]
    fn sync_from_downloads_files() {
        let server = MockVfsServer::start();
        server.add_file("/input.txt", b"hello from proxy");
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        let content = std::fs::read(host_dir.path().join("input.txt")).unwrap();
        assert_eq!(content, b"hello from proxy");
    }

    #[test]
    fn sync_from_creates_subdirectories() {
        let server = MockVfsServer::start();
        server.add_dir("/subdir");
        server.add_file("/file.txt", b"data");
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        assert!(host_dir.path().join("file.txt").exists());
        assert!(host_dir.path().join("subdir").is_dir());
    }

    #[test]
    fn sync_from_handles_empty_vfs() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        let entries: Vec<_> = std::fs::read_dir(host_dir.path()).unwrap().collect();
        assert!(entries.is_empty());
    }

    #[test]
    fn sync_from_handles_multiple_files() {
        let server = MockVfsServer::start();
        server.add_file("/a.txt", b"aaa");
        server.add_file("/b.txt", b"bbbbb");
        server.add_file("/c.txt", b"c");
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(std::fs::read(host_dir.path().join("a.txt")).unwrap(), b"aaa");
        assert_eq!(std::fs::read(host_dir.path().join("b.txt")).unwrap(), b"bbbbb");
        assert_eq!(std::fs::read(host_dir.path().join("c.txt")).unwrap(), b"c");
    }

    #[test]
    fn sync_from_handles_large_file() {
        let server = MockVfsServer::start();
        let large_data: Vec<u8> = (0..200_000).map(|i| (i % 256) as u8).collect();
        server.add_file("/large.bin", &large_data);
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        let content = std::fs::read(host_dir.path().join("large.bin")).unwrap();
        assert_eq!(content.len(), 200_000);
        assert_eq!(content, large_data);
    }

    #[test]
    fn sync_from_closes_file_handles() {
        let server = MockVfsServer::start();
        server.add_file("/f1.txt", b"a");
        server.add_file("/f2.txt", b"b");
        let before = server.close_count();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();
        std::thread::sleep(std::time::Duration::from_millis(20));

        assert!(server.close_count() >= before + 2, "expected at least 2 closes, got {}", server.close_count() - before);
    }

    // ── sync_to_proxy tests ────────────────────────────────────────────

    #[test]
    fn sync_to_uploads_files() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::write(host_dir.path().join("output.txt"), b"from host").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/output.txt").unwrap(), b"from host");
    }

    #[test]
    fn sync_to_creates_subdirectories() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(host_dir.path().join("subdir")).unwrap();
        std::fs::write(host_dir.path().join("file.txt"), b"data").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/file.txt").unwrap(), b"data");
        assert!(server.exists("/subdir"));
    }

    #[test]
    fn sync_to_handles_empty_dir() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();
    }

    #[test]
    fn sync_to_overwrites_existing_with_truncate() {
        let server = MockVfsServer::start();
        server.add_file("/existing.txt", b"old content that should be replaced");
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::write(host_dir.path().join("existing.txt"), b"new").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/existing.txt").unwrap(), b"new");
    }

    #[test]
    fn sync_to_uploads_multiple_files() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::write(host_dir.path().join("x.txt"), b"xx").unwrap();
        std::fs::write(host_dir.path().join("y.txt"), b"yyy").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/x.txt").unwrap(), b"xx");
        assert_eq!(server.read_file("/y.txt").unwrap(), b"yyy");
    }

    #[test]
    fn sync_to_closes_file_handles() {
        let server = MockVfsServer::start();
        let before = server.close_count();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::write(host_dir.path().join("a.txt"), b"a").unwrap();
        std::fs::write(host_dir.path().join("b.txt"), b"b").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();
        std::thread::sleep(std::time::Duration::from_millis(20));

        assert!(server.close_count() >= before + 2);
    }

    // ── round-trip sync tests ──────────────────────────────────────────

    #[test]
    fn sync_roundtrip_preserves_data() {
        let server = MockVfsServer::start();
        server.add_file("/input.txt", b"round trip data");

        let conn1 = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        sync_from_proxy(&conn1, host_dir.path()).unwrap();

        std::fs::write(host_dir.path().join("output.txt"), b"written by host").unwrap();

        let conn2 = proxy_fs::Conn::new(server.socket_path()).unwrap();
        sync_to_proxy(&conn2, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/input.txt").unwrap(), b"round trip data");
        assert_eq!(server.read_file("/output.txt").unwrap(), b"written by host");
    }

    // ── wasip2::run edge cases ─────────────────────────────────────────

    #[test]
    fn run_with_invalid_wasm_returns_error() {
        let result = run(b"not valid wasm", &[]);
        assert!(result.is_err());
        let err_msg = format!("{:#}", result.unwrap_err());
        assert!(err_msg.contains("compile wasm component"), "got: {err_msg}");
    }

    #[test]
    fn run_with_empty_bytes_returns_error() {
        let result = run(b"", &[]);
        assert!(result.is_err());
    }

    // ── nested directory sync tests ────────────────────────────────────

    #[test]
    fn sync_from_downloads_nested_files() {
        let server = MockVfsServer::start();
        server.add_dir("/sub");
        server.add_file("/sub/nested.txt", b"nested content");
        server.add_file("/top.txt", b"top");
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();

        sync_from_proxy(&conn, host_dir.path()).unwrap();

        // Top-level file should be downloaded.
        assert_eq!(std::fs::read(host_dir.path().join("top.txt")).unwrap(), b"top");
        // Nested file should also be downloaded.
        assert!(
            host_dir.path().join("sub").join("nested.txt").exists(),
            "nested file should be synced from proxy"
        );
        assert_eq!(
            std::fs::read(host_dir.path().join("sub").join("nested.txt")).unwrap(),
            b"nested content"
        );
    }

    #[test]
    fn sync_to_uploads_nested_files() {
        let server = MockVfsServer::start();
        let conn = proxy_fs::Conn::new(server.socket_path()).unwrap();
        let host_dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(host_dir.path().join("sub")).unwrap();
        std::fs::write(host_dir.path().join("sub").join("nested.txt"), b"from host nested").unwrap();
        std::fs::write(host_dir.path().join("top.txt"), b"from host top").unwrap();

        sync_to_proxy(&conn, host_dir.path()).unwrap();

        assert_eq!(server.read_file("/top.txt").unwrap(), b"from host top");
        // Nested file should also be uploaded.
        assert_eq!(
            server.read_file("/sub/nested.txt").unwrap_or_default(),
            b"from host nested",
            "nested file should be synced to proxy"
        );
    }

    // ── concurrent access tests ────────────────────────────────────────

    #[test]
    fn concurrent_reads_on_shared_conn() {
        let server = MockVfsServer::start();
        for i in 0..10 {
            server.add_file(&format!("/file{i}.txt"), format!("data{i}").as_bytes());
        }
        let conn = std::sync::Arc::new(proxy_fs::Conn::new(server.socket_path()).unwrap());

        let handles: Vec<_> = (0..10).map(|i| {
            let conn = std::sync::Arc::clone(&conn);
            std::thread::spawn(move || {
                let resp = conn.raw_call(&proxy_fs::WireRequest::path_op(
                    proxy_fs::OP_METADATA,
                    std::path::Path::new(&format!("/file{i}.txt")),
                )).unwrap();
                let meta = resp.meta.unwrap();
                assert!(meta.is_file);
                assert_eq!(meta.len, 5); // "dataN" is 5 bytes
            })
        }).collect();

        for h in handles {
            h.join().expect("thread panicked");
        }
    }

    #[test]
    fn concurrent_writes_on_shared_conn() {
        let server = MockVfsServer::start();
        let conn = std::sync::Arc::new(proxy_fs::Conn::new(server.socket_path()).unwrap());

        let handles: Vec<_> = (0..10).map(|i| {
            let conn = std::sync::Arc::clone(&conn);
            std::thread::spawn(move || {
                // Each thread creates its own file.
                let path = format!("/concurrent{i}.txt");
                let data = format!("thread{i}");
                let open_resp = conn.raw_call(&proxy_fs::WireRequest {
                    op: proxy_fs::OP_OPEN,
                    path: Some(path),
                    to_path: None,
                    open_opts: Some(proxy_fs::WireOpenOpts {
                        read: false, write: true, create: true,
                        create_new: false, append: false, truncate: false,
                    }),
                    handle: 0, data: None, len: 0, seek_from: 0, seek_pos: 0,
                }).unwrap();

                let mut req = proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_WRITE, open_resp.handle);
                req.data = Some(data.into_bytes());
                conn.raw_call(&req).unwrap();

                conn.raw_call(&proxy_fs::WireRequest::handle_op(proxy_fs::OP_FILE_CLOSE, open_resp.handle)).unwrap();
            })
        }).collect();

        for h in handles {
            h.join().expect("thread panicked");
        }

        // Verify all files were written.
        for i in 0..10 {
            let data = server.read_file(&format!("/concurrent{i}.txt"));
            assert!(data.is_some(), "file /concurrent{i}.txt should exist");
            assert_eq!(data.unwrap(), format!("thread{i}").into_bytes());
        }
    }

    // ── connection drop tests ──────────────────────────────────────────

    #[test]
    fn new_conn_after_server_shutdown_fails() {
        let server = MockVfsServer::start();
        let path = server.socket_path().to_string();

        // Verify connection works while server is up.
        let conn = proxy_fs::Conn::new(&path).unwrap();
        assert!(conn.raw_call(&proxy_fs::WireRequest::path_op(
            proxy_fs::OP_METADATA, std::path::Path::new("/")
        )).is_ok());
        drop(conn);

        // Drop the server.
        drop(server);
        std::thread::sleep(std::time::Duration::from_millis(50));

        // New connections should fail.
        assert!(proxy_fs::Conn::new(&path).is_err(), "connect after server shutdown should fail");
    }
}
