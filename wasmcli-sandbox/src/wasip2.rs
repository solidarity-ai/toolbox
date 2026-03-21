use anyhow::{Context, Result};
use wasmtime::{Cache, CacheConfig, Config, Engine, Store};
use wasmtime::component::{Component, Linker, ResourceTable};
use wasmtime_wasi::p2::{IoView, WasiCtx, WasiCtxBuilder, WasiView, pipe::MemoryOutputPipe};
use wasmtime_wasi_http::{WasiHttpCtx, WasiHttpView};

struct WasmState {
    wasi: WasiCtx,
    http: WasiHttpCtx,
    table: ResourceTable,
}

impl IoView for WasmState {
    fn table(&mut self) -> &mut ResourceTable {
        &mut self.table
    }
}

impl WasiView for WasmState {
    fn ctx(&mut self) -> &mut WasiCtx {
        &mut self.wasi
    }
}

impl WasiHttpView for WasmState {
    fn ctx(&mut self) -> &mut WasiHttpCtx {
        &mut self.http
    }
}

pub fn run(wasm_bytes: &[u8], guest_args: &[String]) -> Result<()> {
    let mut config = Config::new();
    config.wasm_component_model(true);
    config.async_support(false);

    // Enable compilation caching so repeated runs skip recompilation.
    if let Ok(cache) = Cache::new(CacheConfig::new()) {
        config.cache(Some(cache));
    }

    let engine = Engine::new(&config).context("create wasmtime engine")?;
    let component = Component::new(&engine, wasm_bytes).context("compile wasm component")?;

    let stdout_pipe = MemoryOutputPipe::new(65536);
    let stderr_pipe = MemoryOutputPipe::new(65536);

    let mut wasi_builder = WasiCtxBuilder::new();
    wasi_builder
        .inherit_env()
        .inherit_network()
        .allow_ip_name_lookup(true)
        .stdout(stdout_pipe.clone())
        .stderr(stderr_pipe.clone())
        .args(guest_args);

    let state = WasmState {
        wasi: wasi_builder.build(),
        http: WasiHttpCtx::new(),
        table: ResourceTable::new(),
    };

    let mut store = Store::new(&engine, state);

    let mut linker = Linker::new(&engine);
    wasmtime_wasi::p2::add_to_linker_sync(&mut linker).context("link wasi")?;
    wasmtime_wasi_http::add_only_http_to_linker_sync(&mut linker).context("link wasi-http")?;

    let command =
        wasmtime_wasi::p2::bindings::sync::Command::instantiate(&mut store, &component, &linker)
            .context("instantiate wasi command component")?;

    let run_result = command.wasi_cli_run().call_run(&mut store);

    let stdout = stdout_pipe.contents();
    let stderr = stderr_pipe.contents();

    print!("{}", String::from_utf8_lossy(&stdout));
    eprint!("{}", String::from_utf8_lossy(&stderr));

    match run_result {
        Ok(Ok(())) => Ok(()),
        Ok(Err(())) => {
            std::process::exit(1);
        }
        Err(err) => Err(err).context("run wasm component"),
    }
}
