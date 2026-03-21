//! WASI P2 filesystem backed by the VFS proxy socket.
//!
//! Provides a `ProxyDir` that can be stored in Wasmtime's resource table
//! and used as a preopened directory. All filesystem operations are routed
//! directly through the ProxyFs socket protocol — no temp dirs needed.

use crate::proxy_fs::{
    Conn, WireOpenOpts, WireRequest, OP_CREATE_DIR, OP_FILE_CLOSE, OP_FILE_READ, OP_FILE_WRITE,
    OP_METADATA, OP_OPEN, OP_READ_DIR, OP_REMOVE_DIR, OP_REMOVE_FILE, OP_RENAME, OP_FILE_FLUSH,
};
use std::sync::Arc;

/// A proxy-backed directory or file descriptor for the WASI filesystem.
pub enum ProxyDescriptor {
    Dir { path: String, conn: Arc<Conn> },
    File { handle: u64, conn: Arc<Conn> },
}

impl Drop for ProxyDescriptor {
    fn drop(&mut self) {
        if let ProxyDescriptor::File { handle, conn } = self {
            let _ = conn.raw_call(&WireRequest::handle_op(OP_FILE_CLOSE, *handle));
        }
    }
}

/// Connect to the VFS proxy and return a root directory descriptor.
pub fn connect(socket_path: &str) -> anyhow::Result<ProxyDescriptor> {
    let conn = Conn::new(socket_path)?;
    Ok(ProxyDescriptor::Dir {
        path: String::new(),
        conn: Arc::new(conn),
    })
}

impl ProxyDescriptor {
    fn conn(&self) -> &Arc<Conn> {
        match self {
            ProxyDescriptor::Dir { conn, .. } => conn,
            ProxyDescriptor::File { conn, .. } => conn,
        }
    }

    fn resolve_path(&self, relative: &str) -> String {
        match self {
            ProxyDescriptor::Dir { path, .. } => {
                if path.is_empty() {
                    format!("/{relative}")
                } else {
                    format!("{path}/{relative}")
                }
            }
            _ => relative.to_string(),
        }
    }

    pub fn is_dir(&self) -> bool {
        matches!(self, ProxyDescriptor::Dir { .. })
    }

    pub fn open_at(
        &self,
        path: &str,
        read: bool,
        write: bool,
        create: bool,
        truncate: bool,
    ) -> std::io::Result<ProxyDescriptor> {
        let full_path = self.resolve_path(path);
        let conn = self.conn();

        // First, check if it's a directory.
        if let Ok(resp) = conn.raw_call(&WireRequest::path_op(OP_METADATA, std::path::Path::new(&full_path))) {
            if let Some(meta) = &resp.meta {
                if meta.is_dir {
                    return Ok(ProxyDescriptor::Dir {
                        path: full_path,
                        conn: Arc::clone(conn),
                    });
                }
            }
        }

        let req = WireRequest {
            op: OP_OPEN,
            path: Some(full_path),
            to_path: None,
            open_opts: Some(WireOpenOpts {
                read,
                write,
                create,
                create_new: false,
                append: false,
                truncate,
            }),
            handle: 0,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        };

        let resp = conn.raw_call(&req)?;
        Ok(ProxyDescriptor::File {
            handle: resp.handle,
            conn: Arc::clone(conn),
        })
    }

    pub fn read_file(&self, len: usize) -> std::io::Result<(Vec<u8>, bool)> {
        let (handle, conn) = match self {
            ProxyDescriptor::File { handle, conn } => (*handle, conn),
            _ => return Err(std::io::Error::new(std::io::ErrorKind::Other, "not a file")),
        };
        let mut req = WireRequest::handle_op(OP_FILE_READ, handle);
        req.len = len as i64;
        let resp = conn.raw_call(&req)?;
        let eof = resp.data.is_empty() && resp.n == 0;
        Ok((resp.data, eof))
    }

    pub fn write_file(&self, data: &[u8]) -> std::io::Result<usize> {
        let (handle, conn) = match self {
            ProxyDescriptor::File { handle, conn } => (*handle, conn),
            _ => return Err(std::io::Error::new(std::io::ErrorKind::Other, "not a file")),
        };
        let mut req = WireRequest::handle_op(OP_FILE_WRITE, handle);
        req.data = Some(data.to_vec());
        let resp = conn.raw_call(&req)?;
        Ok(resp.n as usize)
    }

    pub fn flush_file(&self) -> std::io::Result<()> {
        let (handle, conn) = match self {
            ProxyDescriptor::File { handle, conn } => (*handle, conn),
            _ => return Err(std::io::Error::new(std::io::ErrorKind::Other, "not a file")),
        };
        conn.raw_call(&WireRequest::handle_op(OP_FILE_FLUSH, handle))?;
        Ok(())
    }

    pub fn metadata(&self, path: &str) -> std::io::Result<ProxyMetadata> {
        let full_path = self.resolve_path(path);
        let conn = self.conn();
        let resp = conn.raw_call(&WireRequest::path_op(OP_METADATA, std::path::Path::new(&full_path)))?;
        match resp.meta {
            Some(m) => Ok(ProxyMetadata {
                is_dir: m.is_dir,
                is_file: m.is_file,
                len: m.len,
                modified: m.modified,
            }),
            None => Err(std::io::Error::new(std::io::ErrorKind::Other, "no metadata")),
        }
    }

    pub fn read_dir(&self) -> std::io::Result<Vec<ProxyDirEntry>> {
        let path = match self {
            ProxyDescriptor::Dir { path, .. } => {
                if path.is_empty() { "/" } else { path.as_str() }
            }
            _ => return Err(std::io::Error::new(std::io::ErrorKind::Other, "not a directory")),
        };
        let conn = self.conn();
        let resp = conn.raw_call(&WireRequest::path_op(OP_READ_DIR, std::path::Path::new(path)))?;
        Ok(resp.entries.into_iter().map(|e| ProxyDirEntry {
            name: e.name,
            is_dir: e.meta.is_dir,
            is_file: e.meta.is_file,
            len: e.meta.len,
        }).collect())
    }

    pub fn create_dir_at(&self, path: &str) -> std::io::Result<()> {
        let full_path = self.resolve_path(path);
        self.conn().raw_call(&WireRequest::path_op(OP_CREATE_DIR, std::path::Path::new(&full_path)))?;
        Ok(())
    }

    pub fn remove_dir_at(&self, path: &str) -> std::io::Result<()> {
        let full_path = self.resolve_path(path);
        self.conn().raw_call(&WireRequest::path_op(OP_REMOVE_DIR, std::path::Path::new(&full_path)))?;
        Ok(())
    }

    pub fn remove_file_at(&self, path: &str) -> std::io::Result<()> {
        let full_path = self.resolve_path(path);
        self.conn().raw_call(&WireRequest::path_op(OP_REMOVE_FILE, std::path::Path::new(&full_path)))?;
        Ok(())
    }

    pub fn rename_at(&self, old: &str, new_path: &str) -> std::io::Result<()> {
        let full_old = self.resolve_path(old);
        let full_new = self.resolve_path(new_path);
        let req = WireRequest {
            op: OP_RENAME,
            path: Some(full_old),
            to_path: Some(full_new),
            open_opts: None,
            handle: 0,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        };
        self.conn().raw_call(&req)?;
        Ok(())
    }
}

pub struct ProxyMetadata {
    pub is_dir: bool,
    pub is_file: bool,
    pub len: u64,
    pub modified: u64,
}

pub struct ProxyDirEntry {
    pub name: String,
    pub is_dir: bool,
    pub is_file: bool,
    pub len: u64,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::test_mock_vfs::MockVfsServer;

    // ── Path resolution tests ──────────────────────────────────────────

    #[test]
    fn resolve_path_from_root_prepends_slash() {
        let server = MockVfsServer::start();
        let desc = connect(server.socket_path()).unwrap();
        assert_eq!(desc.resolve_path("file.txt"), "/file.txt");
    }

    #[test]
    fn resolve_path_from_subdir_prepends_parent() {
        let server = MockVfsServer::start();
        server.add_dir("/sub");
        let root = connect(server.socket_path()).unwrap();
        let sub = root.open_at("sub", true, false, false, false).unwrap();
        assert_eq!(sub.resolve_path("child.txt"), "/sub/child.txt");
    }

    #[test]
    fn resolve_path_empty_root() {
        // Root dir has empty path string
        let server = MockVfsServer::start();
        let desc = connect(server.socket_path()).unwrap();
        match &desc {
            ProxyDescriptor::Dir { path, .. } => assert_eq!(path, ""),
            _ => panic!("expected Dir"),
        }
        assert_eq!(desc.resolve_path("x"), "/x");
    }

    #[test]
    fn is_dir_returns_true_for_dir() {
        let server = MockVfsServer::start();
        let desc = connect(server.socket_path()).unwrap();
        assert!(desc.is_dir());
    }

    #[test]
    fn is_dir_returns_false_for_file() {
        let server = MockVfsServer::start();
        server.add_file("/f.txt", b"data");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("f.txt", true, false, false, false).unwrap();
        assert!(!file.is_dir());
    }

    // ── open_at tests ──────────────────────────────────────────────────

    #[test]
    fn open_at_existing_file_returns_file_descriptor() {
        let server = MockVfsServer::start();
        server.add_file("/doc.txt", b"hello");
        let root = connect(server.socket_path()).unwrap();
        let desc = root.open_at("doc.txt", true, false, false, false).unwrap();
        assert!(!desc.is_dir());
    }

    #[test]
    fn open_at_existing_dir_returns_dir_descriptor() {
        let server = MockVfsServer::start();
        server.add_dir("/mydir");
        let root = connect(server.socket_path()).unwrap();
        let desc = root.open_at("mydir", true, false, false, false).unwrap();
        assert!(desc.is_dir());
    }

    #[test]
    fn open_at_with_create_creates_file() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        let desc = root.open_at("new.txt", false, true, true, false).unwrap();
        assert!(!desc.is_dir());
        assert!(server.exists("/new.txt"));
    }

    #[test]
    fn open_at_nonexistent_without_create_fails() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        let result = root.open_at("nope.txt", true, false, false, false);
        assert!(result.is_err());
    }

    #[test]
    fn open_at_on_file_descriptor_fails() {
        let server = MockVfsServer::start();
        server.add_file("/f.txt", b"x");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("f.txt", true, false, false, false).unwrap();
        let result = file.open_at("child", true, false, false, false);
        assert!(result.is_err());
    }

    #[test]
    fn open_at_with_read_write_flags() {
        let server = MockVfsServer::start();
        server.add_file("/rw.txt", b"");
        let root = connect(server.socket_path()).unwrap();
        let desc = root.open_at("rw.txt", true, true, false, false).unwrap();
        assert!(!desc.is_dir());
    }

    #[test]
    fn open_at_with_truncate() {
        let server = MockVfsServer::start();
        server.add_file("/trunc.txt", b"old content");
        let root = connect(server.socket_path()).unwrap();
        let _desc = root.open_at("trunc.txt", true, true, false, true).unwrap();
        assert_eq!(server.read_file("/trunc.txt").unwrap(), b"");
    }

    // ── read/write tests ───────────────────────────────────────────────

    #[test]
    fn read_file_returns_data_and_eof() {
        let server = MockVfsServer::start();
        server.add_file("/read.txt", b"hello");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("read.txt", true, false, false, false).unwrap();
        let (data, eof) = file.read_file(100).unwrap();
        assert_eq!(data, b"hello");
        assert!(!eof);
    }

    #[test]
    fn read_file_at_eof_returns_empty_with_eof_true() {
        let server = MockVfsServer::start();
        server.add_file("/small.txt", b"hi");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("small.txt", true, false, false, false).unwrap();
        let _ = file.read_file(100).unwrap(); // consume all
        let (data, eof) = file.read_file(100).unwrap();
        assert!(data.is_empty());
        assert!(eof);
    }

    #[test]
    fn read_file_on_dir_returns_error() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        let result = root.read_file(100);
        assert!(result.is_err());
    }

    #[test]
    fn write_file_returns_bytes_written() {
        let server = MockVfsServer::start();
        server.add_file("/w.txt", b"");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("w.txt", false, true, false, false).unwrap();
        let n = file.write_file(b"hello").unwrap();
        assert_eq!(n, 5);
        assert_eq!(server.read_file("/w.txt").unwrap(), b"hello");
    }

    #[test]
    fn write_file_on_dir_returns_error() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        let result = root.write_file(b"data");
        assert!(result.is_err());
    }

    #[test]
    fn write_file_empty_data() {
        let server = MockVfsServer::start();
        server.add_file("/empty_w.txt", b"");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("empty_w.txt", false, true, false, false).unwrap();
        let n = file.write_file(b"").unwrap();
        assert_eq!(n, 0);
    }

    #[test]
    fn flush_file_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/fl.txt", b"x");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("fl.txt", true, false, false, false).unwrap();
        assert!(file.flush_file().is_ok());
    }

    #[test]
    fn flush_file_on_dir_returns_error() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        assert!(root.flush_file().is_err());
    }

    // ── directory ops tests ────────────────────────────────────────────

    #[test]
    fn metadata_returns_correct_fields_for_file() {
        let server = MockVfsServer::start();
        server.add_file("/meta.txt", b"abcdef");
        let root = connect(server.socket_path()).unwrap();
        let meta = root.metadata("meta.txt").unwrap();
        assert!(meta.is_file);
        assert!(!meta.is_dir);
        assert_eq!(meta.len, 6);
    }

    #[test]
    fn metadata_returns_correct_fields_for_dir() {
        let server = MockVfsServer::start();
        server.add_dir("/metadir");
        let root = connect(server.socket_path()).unwrap();
        let meta = root.metadata("metadir").unwrap();
        assert!(meta.is_dir);
        assert!(!meta.is_file);
    }

    #[test]
    fn metadata_nonexistent_returns_error() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        assert!(root.metadata("nope").is_err());
    }

    #[test]
    fn read_dir_returns_entries() {
        let server = MockVfsServer::start();
        server.add_file("/a.txt", b"aa");
        server.add_file("/b.txt", b"bbb");
        let root = connect(server.socket_path()).unwrap();
        let entries = root.read_dir().unwrap();
        assert_eq!(entries.len(), 2);
        let names: Vec<&str> = entries.iter().map(|e| e.name.as_str()).collect();
        assert!(names.contains(&"a.txt"));
        assert!(names.contains(&"b.txt"));
    }

    #[test]
    fn read_dir_empty_returns_empty_vec() {
        let server = MockVfsServer::start();
        server.add_dir("/empty");
        let root = connect(server.socket_path()).unwrap();
        let sub = root.open_at("empty", true, false, false, false).unwrap();
        let entries = sub.read_dir().unwrap();
        assert!(entries.is_empty());
    }

    #[test]
    fn read_dir_on_file_returns_error() {
        let server = MockVfsServer::start();
        server.add_file("/f.txt", b"x");
        let root = connect(server.socket_path()).unwrap();
        let file = root.open_at("f.txt", true, false, false, false).unwrap();
        assert!(file.read_dir().is_err());
    }

    #[test]
    fn create_dir_at_succeeds() {
        let server = MockVfsServer::start();
        let root = connect(server.socket_path()).unwrap();
        root.create_dir_at("newdir").unwrap();
        assert!(server.exists("/newdir"));
    }

    #[test]
    fn create_dir_at_existing_returns_error() {
        let server = MockVfsServer::start();
        server.add_dir("/existing");
        let root = connect(server.socket_path()).unwrap();
        assert!(root.create_dir_at("existing").is_err());
    }

    #[test]
    fn remove_dir_at_succeeds() {
        let server = MockVfsServer::start();
        server.add_dir("/rmdir");
        let root = connect(server.socket_path()).unwrap();
        root.remove_dir_at("rmdir").unwrap();
        assert!(!server.exists("/rmdir"));
    }

    #[test]
    fn remove_file_at_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/rmfile.txt", b"bye");
        let root = connect(server.socket_path()).unwrap();
        root.remove_file_at("rmfile.txt").unwrap();
        assert!(!server.exists("/rmfile.txt"));
    }

    #[test]
    fn rename_at_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/old.txt", b"content");
        let root = connect(server.socket_path()).unwrap();
        root.rename_at("old.txt", "new.txt").unwrap();
        assert!(!server.exists("/old.txt"));
        assert_eq!(server.read_file("/new.txt").unwrap(), b"content");
    }

    // ── drop/cleanup tests ─────────────────────────────────────────────

    #[test]
    fn dropping_file_sends_close() {
        let server = MockVfsServer::start();
        server.add_file("/drop.txt", b"x");
        let before = server.close_count();
        {
            let root = connect(server.socket_path()).unwrap();
            let _file = root.open_at("drop.txt", true, false, false, false).unwrap();
            // _file dropped here
        }
        std::thread::sleep(std::time::Duration::from_millis(20));
        assert!(server.close_count() > before);
    }

    #[test]
    fn dropping_dir_does_not_send_close() {
        let server = MockVfsServer::start();
        server.add_dir("/keepdir");
        let before = server.close_count();
        {
            let root = connect(server.socket_path()).unwrap();
            let _sub = root.open_at("keepdir", true, false, false, false).unwrap();
            // _sub (Dir) dropped here — should NOT send close
        }
        std::thread::sleep(std::time::Duration::from_millis(20));
        assert_eq!(server.close_count(), before);
    }

    #[test]
    fn connect_to_valid_socket_returns_dir() {
        let server = MockVfsServer::start();
        let desc = connect(server.socket_path()).unwrap();
        assert!(desc.is_dir());
    }

    #[test]
    fn connect_to_invalid_socket_returns_error() {
        let result = connect("/tmp/nonexistent-proxy-wasi-fs-test.sock");
        assert!(result.is_err());
    }
}
