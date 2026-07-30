# Security revocations

Toolbox has an emergency revocation path for released Toolbox binaries and
immutable package versions. The official policy is served by the package
registry at `GET /v1/security/policy`.

## Revocation identities

- **Toolbox binary:** exact release version, such as `v1.0.0`.
- **Package:** exact module path and version, such as
  `github.com/include-tools/google-workspace@v1.2.3`.

`runtimeStateVersion` is deliberately not a revocation identity. It describes
the persisted runtime-state JSON format, not independently distributed code.
A runtime implementation vulnerability is fixed and revoked by Toolbox binary
version. A bad tool release is revoked by package version.

Archive SHA-256 remains an integrity check, not a revocation identity. The
archive contains an internal manifest with its module path, and Toolbox verifies
that it matches the external manifest. Identical bytes therefore cannot be
validly reused under another module; revoking module and version is the clearer
operational identity.

Package releases are immutable. Do not replace the bytes behind an existing
version to "repair" it; publish a fixed version, then revoke the bad version.
The `@include-tools/toolbox-*` npm platform packages carry that same native
binary, so the embedded Toolbox version revokes every delivery wrapper at
once.

## Client enforcement

Released Toolbox binaries check their own version at process startup. Package
policy is checked:

1. before the resolver trusts a lockfile or touches cached or remote package
   bytes;
2. again before a cached or fetched package is trusted; and
3. immediately before every tool invocation, including invocations in
   long-lived MCP and SDK bridge processes.

Package compatibility is a separate, monotonic check. Source manifests may
declare `minimumToolboxVersion`; `toolbox-pack` writes a required
`manifestSchemaVersion`, preserves or defaults the minimum to its own released
version, and records that release as `packedByToolboxVersion`. Archive loading
requires the running released Toolbox version to be greater than or equal to
the package minimum before Toolbox inspects package TypeScript. Revocations
remain exact module/version matches; they do not reuse compatibility ranges.

The package action installs an exact released `toolbox-pack` version rather
than `main`, so the injected version is reproducible. Distribution manifests
reject unknown fields, while the registry exposes all three compatibility
fields in `.info` metadata and retains the raw manifest as the source of truth.

The policy is cached for 24 hours and shared by Toolbox processes for the same
OS user. Each policy URL has its own cache file under the operating system's
user cache directory; the URL's SHA-256 is used as the filename, and the same
hash is recorded inside the cache to prevent a policy from one registry being
trusted for another. The daemon does not own the policy cache. Writes use an
atomic rename, so a cross-process file lock is unnecessary; simultaneous stale
processes may make a harmless duplicate refresh.

Default policy cache locations are:

- macOS: `~/Library/Caches/toolbox/security-policies/<policy-url-sha256>.json`
- Linux: `${XDG_CACHE_HOME:-~/.cache}/toolbox/security-policies/<policy-url-sha256>.json`
- Windows: `%LocalAppData%\toolbox\security-policies\<policy-url-sha256>.json`

The policy cache file is written with mode `0600` on Unix systems. Downloaded
package archives use the separate `toolbox/pkg` directory under the same user
cache root (or `TOOLBOX_CACHE_DIR` when configured).

If a refresh fails, Toolbox keeps enforcing the last valid cached policy, even
when it is stale. If a machine has never obtained a valid policy and the
registry is unreachable, Toolbox fails open with a warning so cached packages
remain usable offline. A successfully matched revocation always fails closed.
Policy HTTP requests use a 10-second total timeout.

The default policy URL follows `TOOLBOX_REGISTRY`. Setting
`TOOLBOX_REGISTRY=off` also disables the implicit policy lookup. Operators can
set `TOOLBOX_SECURITY_POLICY` to a complete policy URL (or a registry base URL)
to use an organizational mirror, or set it to `off` explicitly.

## Incident runbook

Apply the registry migrations before the first incident. During an incident,
publish the fixed release first whenever possible, then insert one or more
active revocations into the production D1 database.

Block a Toolbox release:

```sql
INSERT INTO security_revocations (kind, version, reason, advisory_url)
VALUES ('toolbox', 'v1.0.0', 'CVE or concise operator reason',
        'https://include.tools/advisories/TBX-2026-001');
```

Block a package version:

```sql
INSERT INTO security_revocations
  (kind, module_path, version, reason, advisory_url)
VALUES
  ('package', 'github.com/include-tools/example', 'v1.2.3',
   'Credential disclosure',
   'https://include.tools/advisories/PKG-2026-001');
```

Withdraw a revocation only after the incident decision is recorded:

```sql
UPDATE security_revocations
SET withdrawn_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE kind = 'package'
  AND lower(module_path) = lower('github.com/include-tools/example')
  AND version = 'v1.2.3'
  AND withdrawn_at IS NULL;
```

Verify the public policy and the blocked package endpoint:

```sh
curl -fsS https://packages.include.tools/v1/security/policy
curl -i https://packages.include.tools/v1/packages/github.com/include-tools/example/@v/v1.2.3.info
```

The second request must return `451` with `error: "revoked"`. Version listings
must omit the revoked version, and `@latest` must resolve to the newest allowed
release. Pinned metadata is revalidated within five minutes so a cached `200`
cannot remain valid indefinitely after a revocation.

For a revoked Toolbox release, also deprecate the corresponding npm versions
so users receive a warning before installation. Cover the root package and
every platform package published for that release:

```sh
npm deprecate @include-tools/toolbox@1.0.0 "Security issue: upgrade to the latest release"
npm deprecate @include-tools/toolbox-darwin-arm64@1.0.0 "Security issue: upgrade"
npm deprecate @include-tools/toolbox-darwin-x64@1.0.0 "Security issue: upgrade"
npm deprecate @include-tools/toolbox-linux-arm64@1.0.0 "Security issue: upgrade"
npm deprecate @include-tools/toolbox-linux-x64@1.0.0 "Security issue: upgrade"
npm deprecate @include-tools/toolbox-win32-x64@1.0.0 "Security issue: upgrade"
```

Npm deprecation is a distribution warning, not the enforcement boundary. The
embedded binary check is what protects already-installed copies.

## Release invariant

The release workflow must embed the git tag into every native binary using the
`internal/buildinfo.releaseVersion` linker variable. Run `toolbox version` on a
release artifact as a smoke test. A release artifact that reports `dev` cannot
match a Toolbox-version revocation and must not be published.
