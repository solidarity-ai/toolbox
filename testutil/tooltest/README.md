# tooltest

Test helpers for toolbox tools. Two HTTP mocking strategies share the same
seam (`toolset.Config.FetchTransport`); pick whichever fits the test.

## FetchMock — hand-written mocks

Use for error injection, synthetic edge cases, and tests where the response
is the point (not fidelity to a real API).

```go
mock := tooltest.NewFetchMock().
    JSON("api.example.com/items", `{"items":[]}`).
    Status("api.example.com/fail", 500)

prepared := tooltest.PrepareToolset(t, decl, toolset.Config{
    FetchTransport: mock,
})
```

## VCR — record-once-replay-forever

Use for faithful round-trips against a real API. Record once with real
credentials (redacted before save), commit the cassette, and every subsequent
run replays from disk. Deterministic, CI-safe, no network.

```go
vcr := tooltest.NewVCR(t, "github_issues")

prepared := tooltest.PrepareToolset(t, decl, toolset.Config{
    FetchTransport: vcr,
})
```

The cassette lives at `testdata/vcr/<name>.json` next to the test file.

### Modes

| Mode | Behavior |
|---|---|
| `replay` (default) | Read from cassette. Missing episodes error. |
| `record` | Proxy to the real API via `http.DefaultTransport`, redact, save on cleanup. |
| `auto` | Record if cassette is missing, otherwise replay. |

Override with the `VCR_MODE` env var or `tooltest.WithMode("record")`.

### Recording with real credentials

Install the credential via the normal CLI path:

```bash
printf '%s\n' "$FIZZY_API_KEY" | toolbox auth ./fizzy --credential api_token --account default
```

Then run the test with `VCR_MODE=record`. VCR's redactor scrubs
`Authorization`, `Cookie`, `Set-Cookie`, and `Proxy-Authorization` before
save. The non-disableable secret scanner refuses to write any cassette that
contains a `Bearer <token>`, a JWT, or a caller-provided `KnownSecrets`
value.

```go
vcr := tooltest.NewVCR(t, "fizzy_happy_path",
    tooltest.WithKnownSecrets(os.Getenv("FIZZY_API_KEY")),
    tooltest.WithIgnoreJSONPaths("$..request_id"),
)
```

### VCR as a mocker

`Handle` injects synthetic episodes for cases the real API can't produce:

```go
vcr := tooltest.NewVCR(t, "combined")
vcr.Handle("GET", "/boom", tooltest.Status(503))
```

### Composition — recorded happy path + injected errors

```go
vcr := tooltest.NewVCR(t, "api_happy_path").
    FallbackTo(tooltest.NewFetchMock().Status("api.example.com/fail", 500))
```

On replay, unmatched requests fall through to the `FetchMock`.

## Safety

Redaction and the secret scanner are always on. The scanner runs on the
serialized cassette bytes *before* writing to disk; if it trips, the test
fails and nothing is written.
