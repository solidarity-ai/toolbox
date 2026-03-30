#![allow(dead_code)]

use anyhow::{Context as _, Result};
use futures::future::BoxFuture;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use std::io::{self, Read as _, Write as _};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use wasmer_wasix::virtual_fs::{
    self, DirEntry, FileOpener, FileType, FsError, Metadata, OpenOptions, OpenOptionsConfig,
    ReadDir, VirtualFile,
};

#[allow(dead_code)]
mod optional_bytes {
    use super::*;
    pub fn serialize<S: Serializer>(
        val: &Option<Vec<u8>>,
        s: S,
    ) -> std::result::Result<S::Ok, S::Error> {
        match val {
            Some(bytes) => serde_bytes::serialize(bytes.as_slice(), s),
            None => s.serialize_none(),
        }
    }
    pub fn deserialize<'de, D: Deserializer<'de>>(
        d: D,
    ) -> std::result::Result<Option<Vec<u8>>, D::Error> {
        let opt: Option<serde_bytes::ByteBuf> = Option::deserialize(d)?;
        Ok(opt.map(|b| b.into_vec()))
    }
}

// Op tags — must match Go side.
pub(crate) const OP_READLINK: i32 = 1;
pub(crate) const OP_READ_DIR: i32 = 2;
pub(crate) const OP_CREATE_DIR: i32 = 3;
pub(crate) const OP_REMOVE_DIR: i32 = 4;
pub(crate) const OP_RENAME: i32 = 5;
pub(crate) const OP_METADATA: i32 = 6;
pub(crate) const OP_SYMLINK_METADATA: i32 = 7;
pub(crate) const OP_REMOVE_FILE: i32 = 8;
pub(crate) const OP_OPEN: i32 = 9;

pub(crate) const OP_FILE_READ: i32 = 20;
pub(crate) const OP_FILE_WRITE: i32 = 21;
pub(crate) const OP_FILE_SEEK: i32 = 22;
pub(crate) const OP_FILE_FLUSH: i32 = 23;
pub(crate) const OP_FILE_CLOSE: i32 = 24;
pub(crate) const OP_FILE_SET_LEN: i32 = 25;

// Error codes — must match Go side.
pub(crate) const ERR_OK: i32 = 0;
pub(crate) const ERR_NOT_FOUND: i32 = 1;
pub(crate) const ERR_ALREADY_EXISTS: i32 = 2;
pub(crate) const ERR_NOT_DIR: i32 = 4;
pub(crate) const ERR_NOT_FILE: i32 = 5;
pub(crate) const ERR_NOT_EMPTY: i32 = 6;
pub(crate) const ERR_IO: i32 = 7;
pub(crate) const ERR_UNSUPPORTED: i32 = 9;

#[derive(Serialize)]
pub(crate) struct WireRequest {
    pub(crate) op: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) to_path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) open_opts: Option<WireOpenOpts>,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub(crate) handle: u64,
    #[serde(skip_serializing_if = "Option::is_none", with = "optional_bytes")]
    pub(crate) data: Option<Vec<u8>>,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub(crate) len: i64,
    #[serde(skip_serializing_if = "is_zero_i32")]
    pub(crate) seek_from: i32,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub(crate) seek_pos: i64,
}

fn is_zero_u64(v: &u64) -> bool {
    *v == 0
}
fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}
fn is_zero_i32(v: &i32) -> bool {
    *v == 0
}

#[derive(Serialize)]
pub(crate) struct WireOpenOpts {
    pub(crate) read: bool,
    pub(crate) write: bool,
    pub(crate) create: bool,
    pub(crate) create_new: bool,
    pub(crate) append: bool,
    pub(crate) truncate: bool,
}

#[derive(Deserialize)]
#[allow(dead_code)]
pub(crate) struct WireResponse {
    #[serde(default)]
    pub(crate) err: i32,
    #[serde(default)]
    pub(crate) target: String,
    #[serde(default)]
    pub(crate) entries: Vec<WireDirEntry>,
    #[serde(default)]
    pub(crate) meta: Option<WireMetadata>,
    #[serde(default)]
    pub(crate) handle: u64,
    #[serde(default, with = "serde_bytes")]
    pub(crate) data: Vec<u8>,
    #[serde(default)]
    pub(crate) pos: i64,
    #[serde(default)]
    pub(crate) n: i32,
}

#[derive(Deserialize)]
pub(crate) struct WireDirEntry {
    pub(crate) name: String,
    pub(crate) meta: WireMetadata,
}

#[derive(Deserialize)]
pub(crate) struct WireMetadata {
    pub(crate) is_dir: bool,
    pub(crate) is_file: bool,
    pub(crate) len: u64,
    pub(crate) accessed: u64,
    pub(crate) created: u64,
    pub(crate) modified: u64,
}

impl WireRequest {
    pub(crate) fn path_op(op: i32, path: &Path) -> Self {
        Self {
            op,
            path: Some(path.to_string_lossy().into_owned()),
            to_path: None,
            open_opts: None,
            handle: 0,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        }
    }

    pub(crate) fn handle_op(op: i32, handle: u64) -> Self {
        Self {
            op,
            path: None,
            to_path: None,
            open_opts: None,
            handle,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        }
    }
}

pub(crate) fn wire_io_err(code: i32) -> std::io::Error {
    let kind = match code {
        ERR_NOT_FOUND => std::io::ErrorKind::NotFound,
        ERR_ALREADY_EXISTS => std::io::ErrorKind::AlreadyExists,
        ERR_NOT_DIR | ERR_NOT_FILE => std::io::ErrorKind::InvalidInput,
        ERR_NOT_EMPTY => std::io::ErrorKind::InvalidInput,
        ERR_UNSUPPORTED => std::io::ErrorKind::Unsupported,
        _ => std::io::ErrorKind::Other,
    };
    std::io::Error::new(kind, format!("vfs error code {code}"))
}

fn wire_err(code: i32) -> FsError {
    match code {
        ERR_NOT_FOUND => FsError::EntryNotFound,
        ERR_ALREADY_EXISTS => FsError::AlreadyExists,
        ERR_NOT_DIR => FsError::BaseNotDirectory,
        ERR_NOT_FILE => FsError::NotAFile,
        ERR_NOT_EMPTY => FsError::DirectoryNotEmpty,
        ERR_IO => FsError::IOError,
        ERR_UNSUPPORTED => FsError::Unsupported,
        _ => FsError::UnknownError,
    }
}

fn wire_metadata(m: &WireMetadata) -> Metadata {
    Metadata {
        ft: FileType {
            dir: m.is_dir,
            file: m.is_file,
            ..Default::default()
        },
        accessed: m.accessed,
        created: m.created,
        modified: m.modified,
        len: m.len,
    }
}

/// A thread-safe UDS connection that serializes access.
pub(crate) struct Conn {
    stream: Mutex<UnixStream>,
}

impl Conn {
    pub(crate) fn new(path: &str) -> Result<Self> {
        let stream =
            UnixStream::connect(path).with_context(|| format!("connect to VFS socket {path}"))?;
        Ok(Self {
            stream: Mutex::new(stream),
        })
    }

    fn call(&self, req: &WireRequest) -> virtual_fs::Result<WireResponse> {
        self.raw_call(req).map_err(|_| FsError::IOError)
    }

    pub(crate) fn raw_call(&self, req: &WireRequest) -> std::io::Result<WireResponse> {
        let data = rmp_serde::to_vec_named(req).map_err(std::io::Error::other)?;
        let mut stream = self
            .stream
            .lock()
            .map_err(|_| std::io::Error::other("lock poisoned"))?;

        // Write length-prefixed frame.
        let len_bytes = (data.len() as u32).to_be_bytes();
        stream.write_all(&len_bytes)?;
        stream.write_all(&data)?;

        // Read response frame.
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf)?;
        let resp_len = u32::from_be_bytes(len_buf) as usize;
        let mut resp_buf = vec![0u8; resp_len];
        stream.read_exact(&mut resp_buf)?;

        let resp: WireResponse = rmp_serde::from_slice(&resp_buf).map_err(std::io::Error::other)?;
        if resp.err != ERR_OK {
            return Err(wire_io_err(resp.err));
        }
        Ok(resp)
    }
}

/// Public alias for shared proxy connections.
pub(crate) type ProxyConn = Conn;

/// ProxyFs implements `virtual_fs::FileSystem` by forwarding all operations
/// over a UDS connection to the Go VFS server.
#[derive(Debug)]
pub struct ProxyFs {
    pub(crate) conn: Arc<Conn>,
}

impl std::fmt::Debug for Conn {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Conn").finish()
    }
}

impl ProxyFs {
    pub fn connect(socket_path: &str) -> Result<Self> {
        let conn = Conn::new(socket_path)?;
        Ok(Self {
            conn: Arc::new(conn),
        })
    }
}

impl virtual_fs::FileSystem for ProxyFs {
    fn readlink(&self, path: &Path) -> virtual_fs::Result<PathBuf> {
        let resp = self.conn.call(&WireRequest::path_op(OP_READLINK, path))?;
        Ok(PathBuf::from(resp.target))
    }

    fn read_dir(&self, path: &Path) -> virtual_fs::Result<ReadDir> {
        let resp = self.conn.call(&WireRequest::path_op(OP_READ_DIR, path))?;
        let parent = path.to_path_buf();
        let entries: Vec<DirEntry> = resp
            .entries
            .iter()
            .map(|e| DirEntry {
                path: parent.join(&e.name),
                metadata: Ok(wire_metadata(&e.meta)),
            })
            .collect();
        Ok(ReadDir::new(entries))
    }

    fn create_dir(&self, path: &Path) -> virtual_fs::Result<()> {
        self.conn.call(&WireRequest::path_op(OP_CREATE_DIR, path))?;
        Ok(())
    }

    fn remove_dir(&self, path: &Path) -> virtual_fs::Result<()> {
        self.conn.call(&WireRequest::path_op(OP_REMOVE_DIR, path))?;
        Ok(())
    }

    fn rename<'a>(&'a self, from: &'a Path, to: &'a Path) -> BoxFuture<'a, virtual_fs::Result<()>> {
        let req = WireRequest {
            op: OP_RENAME,
            path: Some(from.to_string_lossy().into_owned()),
            to_path: Some(to.to_string_lossy().into_owned()),
            open_opts: None,
            handle: 0,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        };
        Box::pin(async move {
            self.conn.call(&req)?;
            Ok(())
        })
    }

    fn metadata(&self, path: &Path) -> virtual_fs::Result<Metadata> {
        let resp = self.conn.call(&WireRequest::path_op(OP_METADATA, path))?;
        match resp.meta {
            Some(m) => Ok(wire_metadata(&m)),
            None => Err(FsError::IOError),
        }
    }

    fn symlink_metadata(&self, path: &Path) -> virtual_fs::Result<Metadata> {
        let resp = self
            .conn
            .call(&WireRequest::path_op(OP_SYMLINK_METADATA, path))?;
        match resp.meta {
            Some(m) => Ok(wire_metadata(&m)),
            None => Err(FsError::IOError),
        }
    }

    fn remove_file(&self, path: &Path) -> virtual_fs::Result<()> {
        self.conn
            .call(&WireRequest::path_op(OP_REMOVE_FILE, path))?;
        Ok(())
    }

    fn new_open_options(&self) -> OpenOptions<'_> {
        OpenOptions::new(self)
    }

    fn mount(
        &self,
        _name: String,
        _path: &Path,
        _fs: Box<dyn virtual_fs::FileSystem + Send + Sync>,
    ) -> virtual_fs::Result<()> {
        Err(FsError::Unsupported)
    }
}

impl FileOpener for ProxyFs {
    fn open(
        &self,
        path: &Path,
        conf: &OpenOptionsConfig,
    ) -> virtual_fs::Result<Box<dyn VirtualFile + Send + Sync + 'static>> {
        let req = WireRequest {
            op: OP_OPEN,
            path: Some(path.to_string_lossy().into_owned()),
            to_path: None,
            open_opts: Some(WireOpenOpts {
                read: conf.read,
                write: conf.write,
                create: conf.create,
                create_new: conf.create_new,
                append: conf.append,
                truncate: conf.truncate,
            }),
            handle: 0,
            data: None,
            len: 0,
            seek_from: 0,
            seek_pos: 0,
        };
        let resp = self.conn.call(&req)?;
        Ok(Box::new(ProxyFile {
            conn: Arc::clone(&self.conn),
            handle: resp.handle,
            writable: conf.write || conf.append,
        }))
    }
}

/// ProxyFile implements `VirtualFile` by forwarding read/write/seek/close over
/// the same UDS connection.
#[derive(Debug)]
struct ProxyFile {
    conn: Arc<Conn>,
    handle: u64,
    writable: bool,
}

impl Drop for ProxyFile {
    fn drop(&mut self) {
        let _ = self
            .conn
            .call(&WireRequest::handle_op(OP_FILE_CLOSE, self.handle));
    }
}

impl VirtualFile for ProxyFile {
    fn last_accessed(&self) -> u64 {
        0
    }

    fn last_modified(&self) -> u64 {
        0
    }

    fn created_time(&self) -> u64 {
        0
    }

    fn size(&self) -> u64 {
        0
    }

    fn set_len(&mut self, new_size: u64) -> virtual_fs::Result<()> {
        let mut req = WireRequest::handle_op(OP_FILE_SET_LEN, self.handle);
        req.len = new_size as i64;
        self.conn.call(&req)?;
        Ok(())
    }

    fn unlink(&mut self) -> virtual_fs::Result<()> {
        Err(FsError::Unsupported)
    }

    fn poll_read_ready(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<usize>> {
        Poll::Ready(Ok(4096))
    }

    fn poll_write_ready(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<usize>> {
        if self.writable {
            Poll::Ready(Ok(4096))
        } else {
            Poll::Ready(Err(io::ErrorKind::PermissionDenied.into()))
        }
    }
}

impl tokio::io::AsyncRead for ProxyFile {
    fn poll_read(
        self: Pin<&mut Self>,
        _cx: &mut Context<'_>,
        buf: &mut tokio::io::ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let me = self.get_mut();
        let mut req = WireRequest::handle_op(OP_FILE_READ, me.handle);
        req.len = buf.remaining() as i64;
        match me.conn.call(&req) {
            Ok(resp) => {
                if resp.data.is_empty() && resp.n == 0 {
                    // EOF
                    return Poll::Ready(Ok(()));
                }
                buf.put_slice(&resp.data);
                Poll::Ready(Ok(()))
            }
            Err(e) => Poll::Ready(Err(e.into())),
        }
    }
}

impl tokio::io::AsyncWrite for ProxyFile {
    fn poll_write(
        self: Pin<&mut Self>,
        _cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let me = self.get_mut();
        let mut req = WireRequest::handle_op(OP_FILE_WRITE, me.handle);
        req.data = Some(buf.to_vec());
        match me.conn.call(&req) {
            Ok(resp) => Poll::Ready(Ok(resp.n as usize)),
            Err(e) => Poll::Ready(Err(e.into())),
        }
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let me = self.get_mut();
        match me
            .conn
            .call(&WireRequest::handle_op(OP_FILE_FLUSH, me.handle))
        {
            Ok(_) => Poll::Ready(Ok(())),
            Err(e) => Poll::Ready(Err(e.into())),
        }
    }

    fn poll_shutdown(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Poll::Ready(Ok(()))
    }
}

impl tokio::io::AsyncSeek for ProxyFile {
    fn start_seek(self: Pin<&mut Self>, position: io::SeekFrom) -> io::Result<()> {
        let me = self.get_mut();
        let (from, offset) = match position {
            io::SeekFrom::Start(n) => (0, n as i64),
            io::SeekFrom::Current(n) => (1, n),
            io::SeekFrom::End(n) => (2, n),
        };
        let mut req = WireRequest::handle_op(OP_FILE_SEEK, me.handle);
        req.seek_from = from;
        req.seek_pos = offset;
        me.conn.call(&req).map_err(|e| -> io::Error { e.into() })?;
        Ok(())
    }

    fn poll_complete(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<u64>> {
        // The seek already completed synchronously in start_seek.
        // We don't have the position cached, but the caller typically doesn't
        // need it. Return 0 as a best-effort.
        Poll::Ready(Ok(0))
    }
}
