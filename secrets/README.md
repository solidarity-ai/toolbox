# secrets

## Purpose

`secrets` provides a key-value secret store for toolbox.

It owns:
- the `SecretStore` interface
- the local age-encrypted file backend (`LocalSecretStore`)
- key validation and error vocabulary (`ErrNotFound`, `ErrInvalidKey`)

Config (coming later) will reference secrets by key, and the store resolves them.

## Who depends on this package

### `toolset`
Will use `secrets` during request-scoped assembly to resolve credential bindings.

### `transport`
Will use `secrets` to materialize credentials for outbound HTTP calls.

### `testutil`
Provides `TestSecretStore`, a plaintext in-memory implementation for use in tests.

## What they use it for

- storing and retrieving credentials, API keys, certificates
- scoped key namespacing (e.g. `system/ca_cert`, `tenant/acme/gws_oauth`)
- test doubles that implement the same interface without encryption or disk I/O

## What this package does not own

- config semantics or how secrets are referenced from config
- credential injection into HTTP requests (that's `transport`)
- request-scoped policy or bindings (that's `toolset`)
- runtime execution
- protocol handling
