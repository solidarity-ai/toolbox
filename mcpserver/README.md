# mcpserver

`mcpserver` wraps Toolbox MCP server instantiation.

## Purpose

This package provides the initial outside-in MCP surface for Toolbox.

Right now it registers two stub MCP tools:

- `tool_discovery_execute`
- `tool_action_execute`

These handlers are intentionally minimal. Their job is to prove:

- the server can be instantiated
- the tools are listed over MCP
- the tools are callable over MCP
- the discovery → action handoff shape works at the protocol boundary

## Current status

The current handlers do **not** invoke real CodeMode yet.

They return deterministic stub responses so integration tests can lock in the
MCP contract before deeper codemode wiring is added.

## Intended evolution

Later, a higher layer will wire:

- `tool_discovery_execute` to discovery-oriented CodeMode
- `tool_action_execute` to action-oriented CodeMode using a `toolboxID`

The tests added alongside this package are meant to stay valuable as that
internal implementation changes.
