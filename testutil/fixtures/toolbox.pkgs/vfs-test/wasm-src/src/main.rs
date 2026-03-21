// Minimal WASI guest for VFS integration testing.
// Supports three modes:
// - default: reads /work/input.txt, writes /work/output.txt with a prefix
// - delete: removes /work/input.txt
// - rename: renames /work/input.txt to /work/renamed.txt
// Compiled with: cargo build --release --target wasm32-wasip1
use std::env;
use std::fs;
use std::io::Write;

fn main() {
    let action = env::args().nth(1).unwrap_or_default();
    if action == "delete" {
        if let Err(e) = fs::remove_file("/work/input.txt") {
            eprintln!("remove /work/input.txt: {e}");
            std::process::exit(1);
        }
        print!("deleted");
        return;
    }

    if action == "rename" {
        if let Err(e) = fs::rename("/work/input.txt", "/work/renamed.txt") {
            eprintln!("rename /work/input.txt: {e}");
            std::process::exit(1);
        }
        print!("renamed");
        return;
    }

    let input = match fs::read_to_string("/work/input.txt") {
        Ok(s) => s,
        Err(e) => {
            eprintln!("read /work/input.txt: {e}");
            std::process::exit(1);
        }
    };
    match fs::OpenOptions::new().write(true).create(true).open("/work/output.txt") {
        Ok(mut f) => {
            if let Err(e) = f.write_all(format!("got: {}", input).as_bytes()) {
                eprintln!("write /work/output.txt: {e}");
                std::process::exit(1);
            }
        }
        Err(e) => {
            eprintln!("open /work/output.txt: {e}");
            std::process::exit(1);
        }
    }
    print!("ok");
}
