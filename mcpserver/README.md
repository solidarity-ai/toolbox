# mcpserver

`mcpserver` exposes one actual MCP tool per visible `invoke` tool.

## Purpose

This package is the first outside-in harness for the direct tool-call path.

For now it intentionally stays small:
- it receives a visible tool list
- it registers one MCP tool per entry
- each handler calls `invoke.Run(...)`

This is enough to lock in the shape of the dataflow before params, bindings,
toolset resolution, or richer execution results are added.
