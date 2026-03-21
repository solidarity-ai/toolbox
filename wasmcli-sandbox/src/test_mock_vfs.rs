//! Mock VFS server for Rust-side testing.
//!
//! Implements the same msgpack-over-UDS protocol as the Go VFS server,
//! backed by an in-memory HashMap filesystem.

use rmp_serde;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::unix::net::UnixListener;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

// Mirror the Go protocol constants.
const OP_READLINK: i32 = 1;
const OP_READ_DIR: i32 = 2;
const OP_CREATE_DIR: i32 = 3;
const OP_REMOVE_DIR: i32 = 4;
const OP_RENAME: i32 = 5;
const OP_METADATA: i32 = 6;
const OP_SYMLINK_METADATA: i32 = 7;
const OP_REMOVE_FILE: i32 = 8;
const OP_OPEN: i32 = 9;

const OP_FILE_READ: i32 = 20;
const OP_FILE_WRITE: i32 = 21;
const OP_FILE_SEEK: i32 = 22;
const OP_FILE_FLUSH: i32 = 23;
const OP_FILE_CLOSE: i32 = 24;
const OP_FILE_SET_LEN: i32 = 25;

const ERR_OK: i32 = 0;
const ERR_NOT_FOUND: i32 = 1;
const ERR_ALREADY_EXISTS: i32 = 2;
const ERR_NOT_DIR: i32 = 4;
const ERR_NOT_FILE: i32 = 5;
const ERR_NOT_EMPTY: i32 = 6;
const ERR_IO: i32 = 7;
const ERR_UNSUPPORTED: i32 = 9;

#[derive(Deserialize, Debug)]
struct MockRequest {
    op: i32,
    #[serde(default)]
    path: String,
    #[serde(default)]
    to_path: String,
    #[serde(default)]
    open_opts: Option<MockOpenOpts>,
    #[serde(default)]
    handle: u64,
    #[serde(default, with = "serde_bytes")]
    data: Vec<u8>,
    #[serde(default)]
    len: i64,
    #[serde(default)]
    seek_from: i32,
    #[serde(default)]
    seek_pos: i64,
}

#[derive(Deserialize, Debug)]
struct MockOpenOpts {
    read: bool,
    write: bool,
    create: bool,
    create_new: bool,
    append: bool,
    truncate: bool,
}

#[derive(Serialize, Default)]
struct MockResponse {
    err: i32,
    #[serde(skip_serializing_if = "String::is_empty")]
    target: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    entries: Vec<MockDirEntry>,
    #[serde(skip_serializing_if = "Option::is_none")]
    meta: Option<MockMetadata>,
    #[serde(skip_serializing_if = "is_zero_u64")]
    handle: u64,
    #[serde(skip_serializing_if = "Vec::is_empty", with = "serde_bytes")]
    data: Vec<u8>,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pos: i64,
    #[serde(skip_serializing_if = "is_zero_i32")]
    n: i32,
}

fn is_zero_u64(v: &u64) -> bool { *v == 0 }
fn is_zero_i64(v: &i64) -> bool { *v == 0 }
fn is_zero_i32(v: &i32) -> bool { *v == 0 }

#[derive(Serialize)]
struct MockDirEntry {
    name: String,
    meta: MockMetadata,
}

#[derive(Serialize, Clone)]
struct MockMetadata {
    is_dir: bool,
    is_file: bool,
    len: u64,
    accessed: u64,
    created: u64,
    modified: u64,
}

#[derive(Clone)]
enum FsNode {
    File(Vec<u8>),
    Dir,
}

struct FileHandle {
    path: String,
    pos: usize,
    writable: bool,
}

struct MockState {
    nodes: HashMap<String, FsNode>,
    next_handle: u64,
    handles: HashMap<u64, FileHandle>,
    close_count: u64,
}

impl MockState {
    fn new() -> Self {
        let mut nodes = HashMap::new();
        nodes.insert("/".to_string(), FsNode::Dir);
        Self {
            nodes,
            next_handle: 0,
            handles: HashMap::new(),
            close_count: 0,
        }
    }

    fn add_file(&mut self, path: &str, data: &[u8]) {
        self.nodes.insert(path.to_string(), FsNode::File(data.to_vec()));
    }

    fn add_dir(&mut self, path: &str) {
        self.nodes.insert(path.to_string(), FsNode::Dir);
    }

    fn dispatch(&mut self, req: MockRequest) -> MockResponse {
        match req.op {
            OP_READLINK => {
                MockResponse { target: format!("/target/of{}", req.path), ..Default::default() }
            }
            OP_READ_DIR => {
                let dir_path = if req.path.is_empty() { "/" } else { &req.path };
                match self.nodes.get(dir_path) {
                    Some(FsNode::Dir) => {
                        let prefix = if dir_path == "/" { "/".to_string() } else { format!("{}/", dir_path) };
                        let entries: Vec<MockDirEntry> = self.nodes.iter()
                            .filter(|(p, _)| {
                                if *p == dir_path { return false; }
                                p.starts_with(&prefix) && !p[prefix.len()..].contains('/')
                            })
                            .map(|(p, node)| {
                                let name = p[prefix.len()..].to_string();
                                let (is_dir, is_file, len) = match node {
                                    FsNode::Dir => (true, false, 0),
                                    FsNode::File(d) => (false, true, d.len() as u64),
                                };
                                MockDirEntry {
                                    name,
                                    meta: MockMetadata { is_dir, is_file, len, accessed: 0, created: 0, modified: 100 },
                                }
                            })
                            .collect();
                        MockResponse { entries, ..Default::default() }
                    }
                    Some(FsNode::File(_)) => MockResponse { err: ERR_NOT_DIR, ..Default::default() },
                    None => MockResponse { err: ERR_NOT_FOUND, ..Default::default() },
                }
            }
            OP_CREATE_DIR => {
                if self.nodes.contains_key(&req.path) {
                    MockResponse { err: ERR_ALREADY_EXISTS, ..Default::default() }
                } else {
                    self.nodes.insert(req.path, FsNode::Dir);
                    MockResponse::default()
                }
            }
            OP_REMOVE_DIR => {
                match self.nodes.get(&req.path) {
                    Some(FsNode::Dir) => {
                        let prefix = format!("{}/", req.path);
                        let has_children = self.nodes.keys().any(|k| k.starts_with(&prefix));
                        if has_children {
                            MockResponse { err: ERR_NOT_EMPTY, ..Default::default() }
                        } else {
                            self.nodes.remove(&req.path);
                            MockResponse::default()
                        }
                    }
                    Some(FsNode::File(_)) => MockResponse { err: ERR_NOT_FILE, ..Default::default() },
                    None => MockResponse { err: ERR_NOT_FOUND, ..Default::default() },
                }
            }
            OP_RENAME => {
                if let Some(node) = self.nodes.remove(&req.path) {
                    self.nodes.insert(req.to_path, node);
                    MockResponse::default()
                } else {
                    MockResponse { err: ERR_NOT_FOUND, ..Default::default() }
                }
            }
            OP_METADATA | OP_SYMLINK_METADATA => {
                match self.nodes.get(&req.path) {
                    Some(FsNode::Dir) => MockResponse {
                        meta: Some(MockMetadata { is_dir: true, is_file: false, len: 0, accessed: 0, created: 0, modified: 100 }),
                        ..Default::default()
                    },
                    Some(FsNode::File(d)) => MockResponse {
                        meta: Some(MockMetadata { is_dir: false, is_file: true, len: d.len() as u64, accessed: 0, created: 0, modified: 200 }),
                        ..Default::default()
                    },
                    None => MockResponse { err: ERR_NOT_FOUND, ..Default::default() },
                }
            }
            OP_REMOVE_FILE => {
                match self.nodes.get(&req.path) {
                    Some(FsNode::File(_)) => { self.nodes.remove(&req.path); MockResponse::default() }
                    Some(FsNode::Dir) => MockResponse { err: ERR_NOT_FILE, ..Default::default() },
                    None => MockResponse { err: ERR_NOT_FOUND, ..Default::default() },
                }
            }
            OP_OPEN => {
                let opts = req.open_opts.as_ref();
                let create = opts.map_or(false, |o| o.create);
                let create_new = opts.map_or(false, |o| o.create_new);
                let truncate = opts.map_or(false, |o| o.truncate);
                let writable = opts.map_or(false, |o| o.write || o.append);

                match self.nodes.get(&req.path) {
                    Some(FsNode::File(_)) if create_new => {
                        MockResponse { err: ERR_ALREADY_EXISTS, ..Default::default() }
                    }
                    Some(FsNode::File(_)) => {
                        if truncate {
                            self.nodes.insert(req.path.clone(), FsNode::File(Vec::new()));
                        }
                        self.next_handle += 1;
                        let h = self.next_handle;
                        self.handles.insert(h, FileHandle { path: req.path, pos: 0, writable });
                        MockResponse { handle: h, ..Default::default() }
                    }
                    Some(FsNode::Dir) => MockResponse { err: ERR_NOT_FILE, ..Default::default() },
                    None if create => {
                        self.nodes.insert(req.path.clone(), FsNode::File(Vec::new()));
                        self.next_handle += 1;
                        let h = self.next_handle;
                        self.handles.insert(h, FileHandle { path: req.path, pos: 0, writable });
                        MockResponse { handle: h, ..Default::default() }
                    }
                    None => MockResponse { err: ERR_NOT_FOUND, ..Default::default() },
                }
            }
            OP_FILE_READ => {
                let h = match self.handles.get(&req.handle) {
                    Some(h) => h,
                    None => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                let path = h.path.clone();
                let pos = h.pos;
                match self.nodes.get(&path) {
                    Some(FsNode::File(data)) => {
                        if pos >= data.len() {
                            MockResponse { data: vec![], n: 0, ..Default::default() }
                        } else {
                            let end = std::cmp::min(pos + req.len as usize, data.len());
                            let chunk = data[pos..end].to_vec();
                            let n = chunk.len() as i32;
                            self.handles.get_mut(&req.handle).unwrap().pos = end;
                            MockResponse { data: chunk, n, ..Default::default() }
                        }
                    }
                    _ => MockResponse { err: ERR_IO, ..Default::default() },
                }
            }
            OP_FILE_WRITE => {
                let h = match self.handles.get(&req.handle) {
                    Some(h) => h,
                    None => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                let path = h.path.clone();
                let pos = h.pos;
                match self.nodes.get_mut(&path) {
                    Some(FsNode::File(data)) => {
                        let write_data = &req.data;
                        let end = pos + write_data.len();
                        if end > data.len() {
                            data.resize(end, 0);
                        }
                        data[pos..end].copy_from_slice(write_data);
                        let n = write_data.len() as i32;
                        self.handles.get_mut(&req.handle).unwrap().pos = end;
                        MockResponse { n, ..Default::default() }
                    }
                    _ => MockResponse { err: ERR_IO, ..Default::default() },
                }
            }
            OP_FILE_SEEK => {
                let h = match self.handles.get_mut(&req.handle) {
                    Some(h) => h,
                    None => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                let path = h.path.clone();
                let file_len = match self.nodes.get(&path) {
                    Some(FsNode::File(d)) => d.len() as i64,
                    _ => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                let new_pos = match req.seek_from {
                    0 => req.seek_pos,
                    1 => h.pos as i64 + req.seek_pos,
                    2 => file_len + req.seek_pos,
                    _ => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                h.pos = new_pos.max(0) as usize;
                MockResponse { pos: new_pos, ..Default::default() }
            }
            OP_FILE_FLUSH => {
                if self.handles.contains_key(&req.handle) {
                    MockResponse::default()
                } else {
                    MockResponse { err: ERR_IO, ..Default::default() }
                }
            }
            OP_FILE_CLOSE => {
                self.handles.remove(&req.handle);
                self.close_count += 1;
                MockResponse::default()
            }
            OP_FILE_SET_LEN => {
                let h = match self.handles.get(&req.handle) {
                    Some(h) => h,
                    None => return MockResponse { err: ERR_IO, ..Default::default() },
                };
                let path = h.path.clone();
                match self.nodes.get_mut(&path) {
                    Some(FsNode::File(data)) => {
                        data.resize(req.len as usize, 0);
                        MockResponse::default()
                    }
                    _ => MockResponse { err: ERR_IO, ..Default::default() },
                }
            }
            _ => MockResponse { err: ERR_UNSUPPORTED, ..Default::default() },
        }
    }
}

/// A mock VFS server that listens on a Unix domain socket.
pub struct MockVfsServer {
    state: Arc<Mutex<MockState>>,
    socket_path: PathBuf,
    _listener_thread: std::thread::JoinHandle<()>,
}

impl MockVfsServer {
    /// Start a mock server. Returns the server and its socket path.
    pub fn start() -> Self {
        let dir = tempfile::tempdir().expect("create temp dir for socket");
        let socket_path = dir.path().join("vfs.sock");
        let listener = UnixListener::bind(&socket_path).expect("bind mock socket");
        let state = Arc::new(Mutex::new(MockState::new()));

        let state_clone = Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            // Keep dir alive so socket path stays valid.
            let _dir = dir;
            for stream in listener.incoming() {
                match stream {
                    Ok(stream) => {
                        let state = Arc::clone(&state_clone);
                        std::thread::spawn(move || handle_conn(stream, state));
                    }
                    Err(_) => break,
                }
            }
        });

        Self {
            state,
            socket_path,
            _listener_thread: thread,
        }
    }

    pub fn socket_path(&self) -> &str {
        self.socket_path.to_str().unwrap()
    }

    /// Pre-populate a file in the mock filesystem.
    pub fn add_file(&self, path: &str, data: &[u8]) {
        self.state.lock().unwrap().add_file(path, data);
    }

    /// Pre-populate a directory in the mock filesystem.
    pub fn add_dir(&self, path: &str) {
        self.state.lock().unwrap().add_dir(path);
    }

    /// Read file data from the mock filesystem (for assertions).
    pub fn read_file(&self, path: &str) -> Option<Vec<u8>> {
        let state = self.state.lock().unwrap();
        match state.nodes.get(path) {
            Some(FsNode::File(d)) => Some(d.clone()),
            _ => None,
        }
    }

    /// Check if a path exists.
    pub fn exists(&self, path: &str) -> bool {
        self.state.lock().unwrap().nodes.contains_key(path)
    }

    /// Get close count (how many OP_FILE_CLOSE ops were received).
    pub fn close_count(&self) -> u64 {
        self.state.lock().unwrap().close_count
    }
}

fn handle_conn(mut stream: std::os::unix::net::UnixStream, state: Arc<Mutex<MockState>>) {
    loop {
        // Read length-prefixed frame.
        let mut len_buf = [0u8; 4];
        if stream.read_exact(&mut len_buf).is_err() {
            return;
        }
        let len = u32::from_be_bytes(len_buf) as usize;
        let mut buf = vec![0u8; len];
        if stream.read_exact(&mut buf).is_err() {
            return;
        }

        let req: MockRequest = match rmp_serde::from_slice(&buf) {
            Ok(r) => r,
            Err(_) => return,
        };

        let resp = state.lock().unwrap().dispatch(req);

        let resp_data = rmp_serde::to_vec_named(&resp).expect("serialize response");
        let resp_len = (resp_data.len() as u32).to_be_bytes();
        if stream.write_all(&resp_len).is_err() { return; }
        if stream.write_all(&resp_data).is_err() { return; }
    }
}
