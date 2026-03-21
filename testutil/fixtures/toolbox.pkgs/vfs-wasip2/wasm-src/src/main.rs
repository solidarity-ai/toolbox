// Minimal WASI P2 guest for VFS integration testing.
// Reads /work/input.txt, writes /work/output.txt with a prefix, prints "ok".
// Compiled with: cargo build --release --target wasm32-wasip2
use std::fs;
use std::io::Write;

fn main() {
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
