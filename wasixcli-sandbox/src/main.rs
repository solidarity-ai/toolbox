mod proxy_fs;

use anyhow::{Context, Result};
use std::env;
use std::fs;
use std::io::Read;
use std::path::PathBuf;
use std::process;
use std::sync::Arc;
use wasmer::{
    sys::{Cranelift, EngineBuilder, Features},
    Module,
};
use wasmer_types::ModuleHash;
use wasmer_wasix::{
    runners::wasi::{RuntimeOrEngine, WasiRunner},
    Pipe,
};

fn main() {
    if let Err(err) = run() {
        eprintln!("wasixcli-sandbox: {err:#}");
        process::exit(1);
    }
}

fn run() -> Result<()> {
    let mut args = env::args().skip(1);
    let wasm_path = args
        .next()
        .context("usage: wasixcli-sandbox <wasm-path | -> [args...]")?;
    let guest_args: Vec<String> = args.collect();

    // Read WASM bytes from stdin when path is "-", otherwise from file.
    let wasm_bytes = if wasm_path == "-" {
        let mut buf = Vec::new();
        std::io::stdin()
            .read_to_end(&mut buf)
            .context("read wasm from stdin")?;
        buf
    } else {
        fs::read(&wasm_path).with_context(|| format!("read wasm from {}", wasm_path))?
    };

    let mut features = Features::new();
    features.exceptions(true);

    let engine: wasmer::Engine = EngineBuilder::new(Cranelift::default())
        .set_features(Some(features))
        .engine()
        .into();

    let module_hash = ModuleHash::xxhash(&wasm_bytes);
    let module = load_or_compile(&engine, &wasm_bytes, &wasm_path, &module_hash)?;

    let (stdout_tx, mut stdout_rx) = Pipe::channel();
    let (stderr_tx, mut stderr_rx) = Pipe::channel();
    let tokio_runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .context("create tokio runtime")?;
    let _guard = tokio_runtime.enter();

    let run_result = {
        let mut runner = WasiRunner::new();
        runner
            .with_args(guest_args.iter().map(String::as_str))
            .with_stdout(Box::new(stdout_tx))
            .with_stderr(Box::new(stderr_tx));

        // Mount the shared VFS if a socket path is provided.
        if let Ok(socket_path) = env::var("TOOLBOX_VFS_SOCK") {
            let proxy = proxy_fs::ProxyFs::connect(&socket_path).context("connect to VFS proxy")?;
            runner.with_mount("/work".to_string(), Arc::new(proxy));
        }

        runner.run_wasm(RuntimeOrEngine::Engine(engine), "tool", module, module_hash)
    };

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

    run_result.context("run wasm module")?;

    Ok(())
}

/// Try to load a pre-compiled module from the cache. If the cache misses,
/// compile from wasm bytes and write the result to the cache for next time.
///
/// Cache key: `<tmp>/toolbox-wasm-cache/<xxhash_of_wasm_bytes>.compiled`
///
/// The xxhash of the raw wasm bytes ensures that any change to the input
/// (different build, different tool version) invalidates the cache entry.
fn load_or_compile(
    engine: &wasmer::Engine,
    wasm_bytes: &[u8],
    wasm_path: &str,
    module_hash: &ModuleHash,
) -> Result<Module> {
    let cache_path = cache_path_for(wasm_path, module_hash);

    // Try the cache first.
    if let Some(module) = try_load_cached(engine, &cache_path) {
        return Ok(module);
    }

    // Compile from source.
    let module = Module::new(engine, wasm_bytes).context("compile wasm module")?;

    // Best-effort write to cache — don't fail the run if caching breaks.
    if let Err(err) = write_cache(&module, &cache_path) {
        eprintln!("wasixcli-sandbox: cache write failed: {err:#}");
    }

    Ok(module)
}

/// Build the cache file path:  <tmp>/toolbox-wasm-cache/<hash>.compiled
///
/// Uses the module hash (xxhash of wasm bytes) so any content change
/// produces a different filename, preventing stale cache hits.
fn cache_path_for(_wasm_path: &str, module_hash: &ModuleHash) -> PathBuf {
    let cache_dir = env::temp_dir().join("toolbox-wasm-cache");
    cache_dir.join(format!("{}.compiled", module_hash))
}

fn try_load_cached(engine: &wasmer::Engine, cache_path: &PathBuf) -> Option<Module> {
    let bytes = fs::read(cache_path).ok()?;
    // SAFETY: We only ever write cache files via Module::serialize from the
    // same engine configuration (Cranelift + identical features). A corrupted
    // or incompatible file will return Err, which we discard.
    unsafe { Module::deserialize(engine, &bytes) }.ok()
}

fn write_cache(module: &Module, cache_path: &PathBuf) -> Result<()> {
    if let Some(parent) = cache_path.parent() {
        fs::create_dir_all(parent).context("create cache directory")?;
    }
    let serialized = module.serialize().context("serialize module")?;
    fs::write(cache_path, &serialized).context("write cache file")?;
    Ok(())
}
