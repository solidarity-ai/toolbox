use anyhow::{Context, Result};
use std::env;
use std::fs;
use std::io::Read;
use std::process;
use wasmer::{Module, Store};
use wasmer_wasix::{Pipe, WasiEnv};

fn main() {
    if let Err(err) = run() {
        eprintln!("wasmersandbox: {err:#}");
        process::exit(1);
    }
}

fn run() -> Result<()> {
    let mut args = env::args().skip(1);
    let wasm_path = args
        .next()
        .context("usage: wasmersandbox <wasm-path> [args...]")?;
    let guest_args: Vec<String> = args.collect();

    let wasm_bytes = fs::read(&wasm_path)
        .with_context(|| format!("read wasm from {}", wasm_path))?;

    let mut store = Store::default();
    let module = Module::new(&store, wasm_bytes).context("compile wasm module")?;

    let (stdout_tx, mut stdout_rx) = Pipe::channel();
    let (stderr_tx, mut stderr_rx) = Pipe::channel();

    let mut wasi = WasiEnv::builder("tool");
    for arg in &guest_args {
        wasi.add_arg(arg);
    }

    wasi.stdout(Box::new(stdout_tx))
        .stderr(Box::new(stderr_tx))
        .run_with_store(module, &mut store)
        .context("run wasm module")?;

    let mut stdout = String::new();
    stdout_rx
        .read_to_string(&mut stdout)
        .context("read guest stdout")?;

    let mut stderr = String::new();
    stderr_rx
        .read_to_string(&mut stderr)
        .context("read guest stderr")?;

    print!("{stdout}");
    eprint!("{stderr}");

    Ok(())
}
