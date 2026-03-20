// Minimal WASI guest for VFS integration testing.
// Reads /input.txt from the VFS proxy and prints its contents to stdout.
// Compiled with: cargo build --release --target wasm32-wasip1
use std::fs;

fn main() {
    match fs::read_to_string("/input.txt") {
        Ok(s) => print!("got: {}", s),
        Err(e) => {
            eprintln!("read error: {e}");
            std::process::exit(1);
        }
    }
}
