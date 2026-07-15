# Local development TLS

`devtls` generates a private development CA and a server certificate for
`localhost`, `127.0.0.1`, and `::1`. It is suitable for the Toolbox-managed
Excel Office.js add-in at `https://localhost:1338`; ports are not part of X.509
identity.

The authoritative bundle and rotation journal—including the CA key and a
recovery copy of the current leaf—are stored as one versioned value in
Toolbox's `secrets.SecretStore`. A separate restricted serving identity
contains only the localhost leaf certificate/key. This split lets a daemon
start HTTPS while the secret store is locked without exposing the CA key.

On macOS and Linux the identity is an atomic PEM file in a `0700` directory
with mode `0600`. On Windows it is encrypted for the current user with DPAPI
and atomically replaced. The leaf key is deliberately treated as less
privileged than the trusted CA key: compromise permits impersonating this
localhost service until rotation, but cannot mint new certificates.

## Generation and saving

Generation is a pure operation: it neither saves nor trusts anything.

```go
bundle, err := devtls.Generate(devtls.Config{})
if err != nil {
	return err
}

// A caller can save the complete portable value itself.
encoded, err := bundle.MarshalBinary()
if err != nil {
	return err
}
err = secretStore.Set(ctx, "toolbox/devtls/office-addin", encoded)

// Or use it directly without writing a key file.
certificate, err := bundle.TLSCertificate()
tlsConfig := &tls.Config{Certificates: []tls.Certificate{certificate}}
```

`ParseBundle` validates an externally loaded serialized bundle. `RootCertPEM`,
`RootKeyPEM`, `CertPEM`, and `KeyPEM` are also available for consumers that need
PEM bytes.

## Managed lifecycle

```go
manager, err := devtls.New(devtls.Config{
	Secrets:      secretStore,
	SecretKey:    "toolbox/devtls/office-addin",
	IdentityPath: identityPath,
	Scope:        devtls.ScopeAuto,
})
if err != nil {
	return err
}

result, err := manager.Install(ctx)
if err != nil {
	return err
}
certificate, err := result.Bundle.TLSCertificate()
```

For a daemon-owned TCP listener, the secret store may remain locked:

```go
tlsListener, err := manager.TLSListener(ctx, listener)
if err != nil {
	return err
}
err = httpServer.Serve(tlsListener)
```

- `Install` atomically saves a missing bundle before attempting native trust,
  repairs or renews leaf material in the same secret, and is idempotent.
- `Save` validates and imports a generated bundle into an empty configured
  secret, writes the restricted leaf identity, and does not change native
  trust; a later `Install` provisions trust.
  It returns `ErrAlreadyInstalled` rather than orphaning an existing trusted CA.
- `Bundle` retrieves a defensive copy of the current saved bundle.
- `TLSConfig`, `TLSListener`, and the standalone `LoadTLSConfig` load only the
  restricted serving identity. They do not access the secret store or invoke
  trust commands.
- `Check` validates keys, signatures, SANs, validity, recovery state, and native
  trust without changing either store.
- `Rotate` journals a pending bundle in the secret before changing trust, commits
  the replacement, and journals old public roots until cleanup succeeds.
- `Remove` removes native trust before deleting the serving identity and secret.
  If authorization is cancelled, both remain available for a safe retry.

The defaults are a five-year P-256 CA, a 90-day P-256 leaf, and renewal 14 days
before expiry. Routine leaf renewal retains the CA, avoiding another trust prompt.

### Daemon mode

A daemon should own and reuse one `Manager`; lifecycle methods on that manager
are serialized for concurrent goroutines. Provisioning (`Install`/`Rotate`) is a
foreground administrative action because macOS, system Windows, and Linux may
need interactive authorization. Daemon startup should use `TLSConfig`,
`TLSListener`, or `LoadTLSConfig`, which never prompt, modify trust, or require
the CA secret store to be unlocked.

Separate `Manager` instances or processes do not have cross-process compare-and-
swap semantics in `secrets.SecretStore`; Toolbox should route lifecycle changes
through the daemon owner. After rotation, replace/reload the server TLS config or
create a new TLS listener to begin serving the new leaf.

The Toolbox daemon's authenticated Unix-domain RPC listener remains plaintext
HTTP/2 over a peer-verified local socket and does not benefit from TLS. Any
loopback TCP HTTP listener—including the approval/debug UI or Office add-in
server—can use this package. Such a UI can load the restricted leaf identity
before secret-store setup/unlock. This removes the need to send the Toolbox
passphrase over a plaintext HTTP bootstrap listener once an identity has been
provisioned.

## Native commands and temporary public files

macOS `security`, Windows `certutil.exe`, and Linux CA-update tools accept a
certificate filename. For those commands, `devtls` creates a private temporary
directory containing only public CA/leaf certificates, invokes the command
without a shell, and removes the directory before returning. The CA key is
never written to disk, included in command arguments, or passed to the
`TrustStore` interface. Tests inspect command inputs and enforce this.

| Platform | Default scope and authorization |
|---|---|
| macOS | Current-user trust. Keychain trust changes can display a SecurityAgent authentication dialog. System scope uses `sudo` plus the System keychain. |
| Windows | Current-user `Root` store, normally without UAC. System scope must run from an already elevated process; the package does not launch a surprise UAC child process. |
| Debian/Ubuntu | System-only portable support via `/usr/local/share/ca-certificates` and `update-ca-certificates`; `sudo` can prompt. |
| Fedora/RHEL | System-only portable support via `/etc/pki/ca-trust/source/anchors` and `update-ca-trust extract`; `sudo` can prompt. |

Set `PrivilegeAlreadyElevated` for an already-root Unix service or container.
Linux `ScopeUser` is rejected because there is no portable per-user trust store.

## Office and browser boundaries

Native TLS trust does not grant Office add-in consent. Office desktop currently
uses Edge WebView2 on Windows and Safari/WKWebView on macOS; Office on the web
uses the active browser. Manifest trust, sideloading, Office Trust Center policy,
and the Windows WebView2 localhost loopback exemption remain separate. Browsers,
Java, containers, and enterprise policy can also use independent CA databases.

See Microsoft's [Office webview documentation](https://learn.microsoft.com/en-us/office/dev/add-ins/concepts/browsers-used-by-office-web-add-ins)
and [Excel development tutorial](https://learn.microsoft.com/en-us/office/dev/add-ins/tutorials/excel-tutorial).

## Native references and validation

- macOS installed `security help add-trusted-cert`, `verify-cert`,
  `remove-trusted-cert`, and `delete-certificate`, plus Apple's [certificate
  trust policy guide](https://support.apple.com/guide/mac-help/mchlp2824/mac)
- Microsoft [`certutil`](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/certutil)
- Debian [`update-ca-certificates(8)`](https://manpages.debian.org/ca-certificates/update-ca-certificates.8.en.html)
- Red Hat [shared system certificates](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/securing_networks/using-shared-system-certificates)

The default suite uses in-memory secret and trust stores and never changes the
host trust store. Platform command tests use a recording runner. Windows and
Linux are cross-built, but real trust prompts, distribution variants, and Office
loading still require validation on those target systems.
