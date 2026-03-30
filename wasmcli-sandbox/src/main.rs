mod proxy_fs;
mod proxy_wasi_fs;
mod wasip2;
mod wasix;

#[cfg(test)]
mod test_mock_vfs;

use anyhow::{bail, Context, Result};
use clap::Parser;
use std::process;

#[derive(Parser)]
#[command(name = "wasmcli-sandbox", trailing_var_arg = true)]
struct Cli {
    /// Runtime to use: wasix-cli or wasip2-cli
    #[arg(long, default_value = "wasix-cli")]
    runtime: String,

    /// Path to the .wasm file
    wasm_path: String,

    /// Arguments to pass to the WASM guest
    #[arg(allow_hyphen_values = true)]
    args: Vec<String>,
}

fn main() {
    if let Err(err) = run() {
        eprintln!("wasmcli-sandbox: {err:#}");
        process::exit(1);
    }
}

fn run() -> Result<()> {
    let cli = Cli::parse();

    let wasm_bytes = if cli.wasm_path == "-" {
        let mut buf = Vec::new();
        std::io::Read::read_to_end(&mut std::io::stdin(), &mut buf)
            .context("read wasm from stdin")?;
        buf
    } else {
        std::fs::read(&cli.wasm_path)
            .with_context(|| format!("read wasm from {}", cli.wasm_path))?
    };

    match cli.runtime.as_str() {
        "wasix-cli" => wasix::run(&wasm_bytes, &cli.args),
        "wasip2-cli" => wasip2::run(&wasm_bytes, &cli.args),
        other => bail!("unknown runtime: {other} (expected wasix-cli or wasip2-cli)"),
    }
}
