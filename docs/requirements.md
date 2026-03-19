# Remote Mode (aka "MCP Only")

- Some type of VFS/secret storage mapping required (sometimes at the tool level, sometimes at the agent level, sometimes at the user level, sometimes at the system level)

# Local Mode

- Also some type of VFS/secret storage mapping

# VFS & Secrets

- effective way to manage secret injection into vfs or env
- vfs shold be able to be shared across runtimes
- flow for authing google workspace tools (i.e. file based auth) or saved env vars, should be clear

# Packaging

- include a version number for package parsing
- automatically register themselves with the registry (unless private)

# Package registry

- automatically downloads a package on demand
- newly published packages to github should be registered automatically

# wasmer+wasix runtime

- HTTP proxying for wasmer is default (very limited networking)

# TS runtime(s) packaging

- Comments should be inferrable from source files
  - As should the metadata for dataAccess and imdepotent

# Code Mode

- Caching Recovery should be a first class feature
