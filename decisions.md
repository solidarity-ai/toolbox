# Auth CLI Decisions

## 2026-05-18: Use the existing Kong CLI shape and credential repository

The auth refresh is implemented as first-class Kong subcommands under `toolbox auth` instead of adding a new parser or command abstraction. The implementation reuses the existing package loader, credential repository, OAuth2 flow, and secret-store lifecycle interfaces so the new UX is package-first without changing credential storage formats.

## 2026-05-18: Auth commands do not auto-initialize the secret store

New `toolbox auth` commands disable the older `--secret-key` auto-setup side effect for ordinary credential reads and writes. First-time setup is intentionally handled by `toolbox auth setup` so recovery codes are shown deliberately and uninitialized stores produce a stable `secret_store_uninitialized` state.
