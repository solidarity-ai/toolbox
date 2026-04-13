# `@include-tools/toolbox`

Thin Node.js wrapper around the `toolbox` Go binary.

It resolves the correct bundled platform binary, starts the hidden
`toolbox _sdkbridge serve-stdio` bridge, and exposes a JS API grouped by the
same nouns as the Go packages: `toolsetfile`, `toolset`, and `tool`.
