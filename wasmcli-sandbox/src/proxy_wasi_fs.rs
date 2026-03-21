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
