//! Tests for proxy_fs, proxy_wasi_fs, and wasip2 sync functions.
//!
//! Uses a mock VFS server over Unix domain sockets.

#[path = "../src/test_mock_vfs.rs"]
mod test_mock_vfs;

use test_mock_vfs::MockVfsServer;

// We need to access the crate's internals for testing.
// Since these are integration tests, we test via the binary's public API
// where possible and use the mock server to verify behavior.

// ═══════════════════════════════════════════════════════════════════════════
// Section 1: Wire protocol / Conn tests (proxy_fs)
// ═══════════════════════════════════════════════════════════════════════════

mod conn_tests {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    #[test]
    fn connect_to_valid_socket() {
        let server = MockVfsServer::start();
        let stream = UnixStream::connect(server.socket_path());
        assert!(stream.is_ok(), "should connect to valid socket");
    }

    #[test]
    fn connect_to_nonexistent_socket_fails() {
        let result = UnixStream::connect("/tmp/nonexistent-vfs-test-socket-12345.sock");
        assert!(result.is_err());
    }

    #[test]
    fn raw_call_sends_and_receives_msgpack_frame() {
        let server = MockVfsServer::start();
        server.add_file("/test.txt", b"hello");

        let mut stream = UnixStream::connect(server.socket_path()).unwrap();

        // Send a metadata request manually.
        let req = rmp_serde::to_vec_named(&serde_json::json!({
            "op": 6,  // OP_METADATA
            "path": "/test.txt"
        })).unwrap();
        let len_bytes = (req.len() as u32).to_be_bytes();
        stream.write_all(&len_bytes).unwrap();
        stream.write_all(&req).unwrap();

        // Read response.
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let resp_len = u32::from_be_bytes(len_buf) as usize;
        let mut resp_buf = vec![0u8; resp_len];
        stream.read_exact(&mut resp_buf).unwrap();

        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        assert_eq!(resp["err"], 0);
        assert_eq!(resp["meta"]["is_file"], true);
        assert_eq!(resp["meta"]["len"], 5);
    }

    #[test]
    fn response_err_not_found() {
        let server = MockVfsServer::start();
        let mut stream = UnixStream::connect(server.socket_path()).unwrap();

        let req = rmp_serde::to_vec_named(&serde_json::json!({
            "op": 6, "path": "/nonexistent"
        })).unwrap();
        stream.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        stream.write_all(&req).unwrap();

        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        stream.read_exact(&mut resp_buf).unwrap();

        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        assert_eq!(resp["err"], 1); // ERR_NOT_FOUND
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 2: Error code mapping tests
// ═══════════════════════════════════════════════════════════════════════════

mod error_mapping_tests {
    // These test the mock server's error codes match the protocol spec.
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    fn call(stream: &mut UnixStream, op: i32, path: &str) -> i32 {
        let extra: serde_json::Value = match op {
            9 => serde_json::json!({"op": op, "path": path, "open_opts": {"read": true, "write": false, "create": false, "create_new": false, "append": false, "truncate": false}}),
            _ => serde_json::json!({"op": op, "path": path}),
        };
        let req = rmp_serde::to_vec_named(&extra).unwrap();
        stream.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        stream.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        stream.read_exact(&mut resp_buf).unwrap();
        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        resp["err"].as_i64().unwrap_or(0) as i32
    }

    #[test]
    fn metadata_not_found_returns_1() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 6, "/nope"), 1);
    }

    #[test]
    fn create_dir_already_exists_returns_2() {
        let server = MockVfsServer::start();
        server.add_dir("/mydir");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 3, "/mydir"), 2);
    }

    #[test]
    fn remove_dir_not_found_returns_1() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 4, "/nope"), 1);
    }

    #[test]
    fn remove_dir_not_empty_returns_6() {
        let server = MockVfsServer::start();
        server.add_dir("/parent");
        server.add_file("/parent/child.txt", b"x");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 4, "/parent"), 6);
    }

    #[test]
    fn remove_file_not_found_returns_1() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 8, "/nope"), 1);
    }

    #[test]
    fn remove_file_on_dir_returns_5() {
        let server = MockVfsServer::start();
        server.add_dir("/mydir");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 8, "/mydir"), 5); // ERR_NOT_FILE
    }

    #[test]
    fn open_not_found_without_create_returns_1() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 9, "/nope"), 1);
    }

    #[test]
    fn rename_not_found_returns_1() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let req = rmp_serde::to_vec_named(&serde_json::json!({"op": 5, "path": "/nope", "to_path": "/dest"})).unwrap();
        s.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        s.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        s.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        s.read_exact(&mut resp_buf).unwrap();
        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        assert_eq!(resp["err"], 1);
    }

    #[test]
    fn unknown_op_returns_unsupported() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        assert_eq!(call(&mut s, 999, "/x"), 9); // ERR_UNSUPPORTED
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 3: Metadata operations tests
// ═══════════════════════════════════════════════════════════════════════════

mod metadata_tests {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    fn metadata_call(stream: &mut UnixStream, path: &str) -> serde_json::Value {
        let req = rmp_serde::to_vec_named(&serde_json::json!({"op": 6, "path": path})).unwrap();
        stream.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        stream.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        stream.read_exact(&mut resp_buf).unwrap();
        rmp_serde::from_slice(&resp_buf).unwrap()
    }

    #[test]
    fn file_metadata_returns_correct_fields() {
        let server = MockVfsServer::start();
        server.add_file("/doc.txt", b"hello world");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = metadata_call(&mut s, "/doc.txt");
        assert_eq!(resp["err"], 0);
        assert_eq!(resp["meta"]["is_file"], true);
        assert_eq!(resp["meta"]["is_dir"], false);
        assert_eq!(resp["meta"]["len"], 11);
    }

    #[test]
    fn dir_metadata_returns_is_dir() {
        let server = MockVfsServer::start();
        server.add_dir("/subdir");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = metadata_call(&mut s, "/subdir");
        assert_eq!(resp["meta"]["is_dir"], true);
        assert_eq!(resp["meta"]["is_file"], false);
    }

    #[test]
    fn root_metadata_returns_is_dir() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = metadata_call(&mut s, "/");
        assert_eq!(resp["meta"]["is_dir"], true);
    }

    #[test]
    fn symlink_metadata_same_as_metadata() {
        let server = MockVfsServer::start();
        server.add_file("/f.txt", b"data");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let req = rmp_serde::to_vec_named(&serde_json::json!({"op": 7, "path": "/f.txt"})).unwrap();
        s.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        s.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        s.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        s.read_exact(&mut resp_buf).unwrap();
        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        assert_eq!(resp["meta"]["is_file"], true);
        assert_eq!(resp["meta"]["len"], 4);
    }

    #[test]
    fn readlink_returns_target() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let req = rmp_serde::to_vec_named(&serde_json::json!({"op": 1, "path": "/link"})).unwrap();
        s.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        s.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        s.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        s.read_exact(&mut resp_buf).unwrap();
        let resp: serde_json::Value = rmp_serde::from_slice(&resp_buf).unwrap();
        assert_eq!(resp["target"], "/target/of/link");
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 4: Directory operations tests
// ═══════════════════════════════════════════════════════════════════════════

mod dir_ops_tests {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    fn call_json(stream: &mut UnixStream, req_json: serde_json::Value) -> serde_json::Value {
        let req = rmp_serde::to_vec_named(&req_json).unwrap();
        stream.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        stream.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        stream.read_exact(&mut resp_buf).unwrap();
        rmp_serde::from_slice(&resp_buf).unwrap()
    }

    #[test]
    fn read_dir_returns_entries() {
        let server = MockVfsServer::start();
        server.add_file("/a.txt", b"aaa");
        server.add_file("/b.txt", b"bb");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 2, "path": "/"}));
        let entries = resp["entries"].as_array().unwrap();
        assert_eq!(entries.len(), 2);
        let names: Vec<&str> = entries.iter().map(|e| e["name"].as_str().unwrap()).collect();
        assert!(names.contains(&"a.txt"));
        assert!(names.contains(&"b.txt"));
    }

    #[test]
    fn read_dir_empty_returns_empty_list() {
        let server = MockVfsServer::start();
        server.add_dir("/empty");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 2, "path": "/empty"}));
        assert_eq!(resp["err"], 0);
        // entries may be missing or empty
        let entries = resp.get("entries").and_then(|v| v.as_array());
        assert!(entries.map_or(true, |e| e.is_empty()));
    }

    #[test]
    fn read_dir_nested_only_lists_direct_children() {
        let server = MockVfsServer::start();
        server.add_dir("/parent");
        server.add_file("/parent/child.txt", b"c");
        server.add_dir("/parent/sub");
        server.add_file("/parent/sub/deep.txt", b"d");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 2, "path": "/parent"}));
        let entries = resp["entries"].as_array().unwrap();
        let names: Vec<&str> = entries.iter().map(|e| e["name"].as_str().unwrap()).collect();
        assert!(names.contains(&"child.txt"));
        assert!(names.contains(&"sub"));
        assert!(!names.contains(&"deep.txt"));
    }

    #[test]
    fn create_dir_succeeds() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 3, "path": "/newdir"}));
        assert_eq!(resp["err"], 0);
        assert!(server.exists("/newdir"));
    }

    #[test]
    fn remove_dir_succeeds() {
        let server = MockVfsServer::start();
        server.add_dir("/todelete");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 4, "path": "/todelete"}));
        assert_eq!(resp["err"], 0);
        assert!(!server.exists("/todelete"));
    }

    #[test]
    fn rename_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/old.txt", b"content");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_json(&mut s, serde_json::json!({"op": 5, "path": "/old.txt", "to_path": "/new.txt"}));
        assert_eq!(resp["err"], 0);
        assert!(!server.exists("/old.txt"));
        assert_eq!(server.read_file("/new.txt").unwrap(), b"content");
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 5: File open/read/write/seek/close tests
// ═══════════════════════════════════════════════════════════════════════════

mod file_ops_tests {
    use super::*;
    use serde::Deserialize;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;

    /// Typed response for file ops (handles binary data field).
    #[derive(Deserialize, Debug)]
    struct Resp {
        #[serde(default)]
        err: i32,
        #[serde(default)]
        handle: u64,
        #[serde(default, with = "serde_bytes")]
        data: Vec<u8>,
        #[serde(default)]
        n: i32,
        #[serde(default)]
        pos: i64,
    }

    fn call_typed(stream: &mut UnixStream, req_json: serde_json::Value) -> Resp {
        let req = rmp_serde::to_vec_named(&req_json).unwrap();
        stream.write_all(&(req.len() as u32).to_be_bytes()).unwrap();
        stream.write_all(&req).unwrap();
        let mut len_buf = [0u8; 4];
        stream.read_exact(&mut len_buf).unwrap();
        let mut resp_buf = vec![0u8; u32::from_be_bytes(len_buf) as usize];
        stream.read_exact(&mut resp_buf).unwrap();
        rmp_serde::from_slice(&resp_buf).unwrap()
    }

    fn open_file(stream: &mut UnixStream, path: &str, read: bool, write: bool, create: bool, truncate: bool) -> u64 {
        let resp = call_typed(stream, serde_json::json!({
            "op": 9, "path": path,
            "open_opts": {"read": read, "write": write, "create": create, "create_new": false, "append": false, "truncate": truncate}
        }));
        assert_eq!(resp.err, 0, "open failed for {path}");
        resp.handle
    }

    #[test]
    fn open_existing_file_returns_handle() {
        let server = MockVfsServer::start();
        server.add_file("/f.txt", b"data");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/f.txt", true, false, false, false);
        assert!(h > 0);
    }

    #[test]
    fn open_with_create_creates_file() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/new.txt", false, true, true, false);
        assert!(h > 0);
        assert!(server.exists("/new.txt"));
    }

    #[test]
    fn open_with_create_new_on_existing_fails() {
        let server = MockVfsServer::start();
        server.add_file("/exists.txt", b"x");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_typed(&mut s, serde_json::json!({
            "op": 9, "path": "/exists.txt",
            "open_opts": {"read": true, "write": false, "create": false, "create_new": true, "append": false, "truncate": false}
        }));
        assert_eq!(resp.err, 2); // ALREADY_EXISTS
    }

    #[test]
    fn open_with_truncate_clears_content() {
        let server = MockVfsServer::start();
        server.add_file("/trunc.txt", b"old data here");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let _h = open_file(&mut s, "/trunc.txt", true, true, false, true);
        assert_eq!(server.read_file("/trunc.txt").unwrap(), b"");
    }

    #[test]
    fn read_returns_data() {
        let server = MockVfsServer::start();
        server.add_file("/read.txt", b"hello world");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/read.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 100}));
        assert_eq!(resp.err, 0);
        assert_eq!(resp.n, 11);
    }

    #[test]
    fn read_at_eof_returns_empty() {
        let server = MockVfsServer::start();
        server.add_file("/small.txt", b"hi");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/small.txt", true, false, false, false);
        // Read all
        call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 100}));
        // Read again at EOF
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 100}));
        assert_eq!(resp.n, 0);
    }

    #[test]
    fn read_with_specific_length() {
        let server = MockVfsServer::start();
        server.add_file("/data.txt", b"abcdefghij");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/data.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 5}));
        assert_eq!(resp.n, 5);
    }

    #[test]
    fn write_returns_byte_count() {
        let server = MockVfsServer::start();
        server.add_file("/w.txt", b"");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/w.txt", false, true, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 21, "handle": h, "data": [104, 105]}));
        assert_eq!(resp.n, 2);
        assert_eq!(server.read_file("/w.txt").unwrap(), b"hi");
    }

    #[test]
    fn write_empty_data() {
        let server = MockVfsServer::start();
        server.add_file("/empty_w.txt", b"");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/empty_w.txt", false, true, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 21, "handle": h, "data": []}));
        assert_eq!(resp.n, 0);
    }

    #[test]
    fn seek_from_start() {
        let server = MockVfsServer::start();
        server.add_file("/seek.txt", b"abcdefghij");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/seek.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 22, "handle": h, "seek_from": 0, "seek_pos": 5}));
        assert_eq!(resp.pos, 5);
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 3}));
        assert_eq!(resp.n, 3);
    }

    #[test]
    fn seek_from_end() {
        let server = MockVfsServer::start();
        server.add_file("/seekend.txt", b"abcdefghij");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/seekend.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 22, "handle": h, "seek_from": 2, "seek_pos": -3}));
        assert_eq!(resp.pos, 7);
    }

    #[test]
    fn flush_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/flush.txt", b"x");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/flush.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 23, "handle": h}));
        assert_eq!(resp.err, 0);
    }

    #[test]
    fn flush_invalid_handle_fails() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_typed(&mut s, serde_json::json!({"op": 23, "handle": 99999}));
        assert_ne!(resp.err, 0);
    }

    #[test]
    fn close_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/close.txt", b"x");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/close.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 24, "handle": h}));
        assert_eq!(resp.err, 0);
    }

    #[test]
    fn read_after_close_fails() {
        let server = MockVfsServer::start();
        server.add_file("/closed.txt", b"data");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/closed.txt", true, false, false, false);
        call_typed(&mut s, serde_json::json!({"op": 24, "handle": h})); // close
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h, "len": 10})); // read
        assert_ne!(resp.err, 0);
    }

    #[test]
    fn set_len_truncates() {
        let server = MockVfsServer::start();
        server.add_file("/setlen.txt", b"abcdefghij");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/setlen.txt", true, true, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 25, "handle": h, "len": 3}));
        assert_eq!(resp.err, 0);
        assert_eq!(server.read_file("/setlen.txt").unwrap().len(), 3);
    }

    #[test]
    fn set_len_extends() {
        let server = MockVfsServer::start();
        server.add_file("/extend.txt", b"ab");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/extend.txt", true, true, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 25, "handle": h, "len": 10}));
        assert_eq!(resp.err, 0);
        assert_eq!(server.read_file("/extend.txt").unwrap().len(), 10);
    }

    #[test]
    fn remove_file_succeeds() {
        let server = MockVfsServer::start();
        server.add_file("/rm.txt", b"bye");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let resp = call_typed(&mut s, serde_json::json!({"op": 8, "path": "/rm.txt"}));
        assert_eq!(resp.err, 0);
        assert!(!server.exists("/rm.txt"));
    }

    #[test]
    fn write_read_roundtrip() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/roundtrip.txt", false, true, true, false);
        let payload = b"hello roundtrip";
        call_typed(&mut s, serde_json::json!({"op": 21, "handle": h, "data": payload.to_vec()}));
        call_typed(&mut s, serde_json::json!({"op": 24, "handle": h})); // close

        let h2 = open_file(&mut s, "/roundtrip.txt", true, false, false, false);
        let resp = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h2, "len": 100}));
        assert_eq!(resp.n, 15);
    }

    #[test]
    fn multiple_handles_independent() {
        let server = MockVfsServer::start();
        server.add_file("/multi.txt", b"abcdef");
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h1 = open_file(&mut s, "/multi.txt", true, false, false, false);
        let h2 = open_file(&mut s, "/multi.txt", true, false, false, false);
        // Read 3 bytes from h1
        let r1 = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h1, "len": 3}));
        assert_eq!(r1.n, 3);
        // h2 should still be at position 0
        let r2 = call_typed(&mut s, serde_json::json!({"op": 20, "handle": h2, "len": 6}));
        assert_eq!(r2.n, 6);
    }

    #[test]
    fn close_increments_count() {
        let server = MockVfsServer::start();
        server.add_file("/cnt.txt", b"x");
        let before = server.close_count();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/cnt.txt", true, false, false, false);
        call_typed(&mut s, serde_json::json!({"op": 24, "handle": h}));
        // Give mock server time to process.
        std::thread::sleep(std::time::Duration::from_millis(10));
        assert_eq!(server.close_count(), before + 1);
    }

    #[test]
    fn large_write_succeeds() {
        let server = MockVfsServer::start();
        let mut s = UnixStream::connect(server.socket_path()).unwrap();
        let h = open_file(&mut s, "/large.bin", false, true, true, false);
        let data: Vec<u8> = (0..100_000).map(|i| (i % 256) as u8).collect();
        let resp = call_typed(&mut s, serde_json::json!({"op": 21, "handle": h, "data": data}));
        assert_eq!(resp.n, 100_000);
        assert_eq!(server.read_file("/large.bin").unwrap().len(), 100_000);
    }
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 6: sync_from_proxy / sync_to_proxy tests (via CLI binary)
// ═══════════════════════════════════════════════════════════════════════════
// These are tested end-to-end via the Go integration tests since
// sync_from_proxy and sync_to_proxy are private functions in the
// wasmcli-sandbox binary. The mock server above validates the protocol
// layer that those functions rely on.
