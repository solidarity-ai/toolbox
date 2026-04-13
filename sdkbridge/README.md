# sdkbridge

`sdkbridge` is the machine-facing stdio JSON-RPC bridge used by the npm
wrapper and future host integrations.

It owns:

- NDJSON-framed JSON-RPC 2.0 over stdio
- `toolsetfile.load` / `toolsetfile.write`
- `toolset.compose` / `toolset.close`
- `toolset.search` / `toolset.inspect`
- `toolset.install` / `toolset.uninstall` / `toolset.auth`
- `tool.invoke`
- bridge lifecycle helpers like `system.version` and `bridge.shutdown`

`cmd/toolbox` should stay thin and only boot this package via the hidden
`toolbox _sdkbridge serve-stdio` entrypoint.
