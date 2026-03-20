mod proxy_fs;

use anyhow::{Context, Result};
use std::env;
use std::fs;
use std::io::Read;
use std::process;
use std::sync::Arc;
use wasmer::{
    sys::{Cranelift, EngineBuilder, Features},
    Module,
};
use wasmer_types::ModuleHash;
use wasmer_wasix::{runners::wasi::{RuntimeOrEngine, WasiRunner}, Pipe};

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

    let mut features = Features::new();
    features.exceptions(true);

    let engine: wasmer::Engine = EngineBuilder::new(Cranelift::default())
        .set_features(Some(features))
        .engine()
        .into();
    let module = Module::new(&engine, &wasm_bytes).context("compile wasm module")?;
    let module_hash = ModuleHash::xxhash(&wasm_bytes);

    let (stdout_tx, mut stdout_rx) = Pipe::channel();
    let (stderr_tx, mut stderr_rx) = Pipe::channel();
    let tokio_runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .context("create tokio runtime")?;
    let _guard = tokio_runtime.enter();

    {
        let mut runner = WasiRunner::new();
        runner
            .with_args(guest_args.iter().map(String::as_str))
            .with_stdout(Box::new(stdout_tx))
            .with_stderr(Box::new(stderr_tx));

        // Mount the shared VFS if a socket path is provided.
        if let Ok(socket_path) = env::var("TOOLBOX_VFS_SOCK") {
            let proxy = proxy_fs::ProxyFs::connect(&socket_path)
                .context("connect to VFS proxy")?;
            runner.with_mount("/".to_string(), Arc::new(proxy));
        }

        runner
            .run_wasm(
                RuntimeOrEngine::Engine(engine),
                "tool",
                module,
                module_hash,
            )
            .context("run wasm module")?;
    }

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
