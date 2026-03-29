# Packaging Redesign Plan

Split the monolithic `packaging/` package into focused subpackages, introduce a compiled archive format (`.toolbox.pkg`), and add a standalone `toolbox-pack` CLI.

## 1. Module layout

After the redesign the `packaging/` tree looks like this:

```
packaging/
├── packaging.go                   # thin top-level coordination API, re-exports LoadedPackage
├── internal/
│   └── pkgtype/
│       └── pkgtype.go             # LoadedPackage type (shared by source, archive, top-level)
├── manifest/
│   ├── manifest.go                # types, parse, validate, compile
│   ├── infer.go                   # inferEffect, inferVerb, inferToolName, inferToolDescription
│   ├── infer_test.go
│   ├── manifest_test.go
│   ├── toolbox_devpkg.schema.json # dev schema (renamed from toolbox_pkg.dev.schema.json)
│   └── toolbox_pkg.schema.json    # dist schema (renamed from toolbox_pkg.dist.schema.json)
├── archive/
│   ├── archive.go                 # Pack / Unpack / Verify
│   └── archive_test.go
└── source/
    ├── source.go                  # LoadSourceDir, LoadSourcePackage
    ├── sourcefs.go                # sourceFS implementation (moved from packaging/sourcefs.go)
    └── source_test.go

cmd/
└── toolbox-pack/
    ├── go.mod                     # standalone module
    ├── go.sum
    └── main.go                    # CLI entry point
```

## 2. Type definitions

### 2.1 Manifest types (`packaging/manifest`)

```go
package manifest

import tooldef "github.com/solidarity-ai/toolbox/tool"

// DevManifest is the shape of toolbox.devpkg.json — the authoring format.
// Fields like Idempotent and Effect may be omitted and will be inferred.
type DevManifest struct {
    Name                      string         `json:"name"`
    Runtime                   tooldef.ToolRuntime `json:"runtime"`
    AdditionalTypeScriptGlobs []string       `json:"additionalTypeScriptGlobs,omitempty"`
    Executables               map[string]string `json:"executables,omitempty"`
    Tools                     []DevTool      `json:"tools"`
}

// DevTool is a single tool entry in the dev manifest.
// Effect and Idempotent are optional — they get inferred during compilation.
type DevTool struct {
    EntryTS    string              `json:"entry_ts"`
    Idempotent *bool               `json:"idempotent,omitempty"`
    Effect *tooldef.Effect `json:"effect,omitempty"`
}

// PkgManifest is the shape of toolbox.pkg.json — the compiled/published format.
// All fields are required. This is what ships in archives and what scanners
// look for when discovering packages on GitHub.
type PkgManifest struct {
    Name                      string         `json:"name"`
    Runtime                   tooldef.ToolRuntime `json:"runtime"`
    AdditionalTypeScriptGlobs []string       `json:"additionalTypeScriptGlobs,omitempty"`
    Executables               map[string]string `json:"executables,omitempty"`
    Tools                     []PkgTool      `json:"tools"`
    SHA256                    string         `json:"sha256,omitempty"` // only on external manifest
}

// PkgTool is a tool in the compiled manifest. All metadata is required.
type PkgTool struct {
    EntryTS    string            `json:"entry_ts"`
    Idempotent bool              `json:"idempotent"`
    Effect tooldef.Effect `json:"effect"`
}
```

### 2.2 Existing types preserved

`tooldef.Package`, `tooldef.PackageTool`, `tooldef.ResolvedTool` remain unchanged. `manifest.Compile` produces a `tooldef.Package` just as `compilePackage` does today.

`LoadedPackage` gains one new field — `Executables` — so that `ResolvedTools()` no longer needs to re-read the manifest from disk:

```go
type LoadedPackage struct {
    Package     tooldef.Package
    Files       fs.FS
    Dir         string
    Executables map[string]string // populated from manifest; used by ResolvedTools() for TSWasmToolDef
}
```

### 2.3 Validation types (stay in `manifest`)

```go
type ValidationMode int

const (
    ValidationModeDev  ValidationMode = iota
    ValidationModeDist
)

type Warning struct {
    Message string
}

type LoadResult struct {
    Package  tooldef.Package
    Warnings []Warning
}
```

## 3. Key function signatures

### 3.1 `packaging/manifest`

```go
// Parse reads raw JSON bytes and returns a DevManifest.
func ParseDev(data []byte) (DevManifest, error)

// ParsePkg reads raw JSON bytes and returns a PkgManifest.
func ParsePkg(data []byte) (PkgManifest, error)

// ValidateDev validates raw JSON against the dev schema.
func ValidateDev(data []byte) error

// ValidatePkg validates raw JSON against the dist/pkg schema.
func ValidatePkg(data []byte) error

// Compile converts a DevManifest into a tooldef.Package.
// Infers effect and other fields where omitted.
func Compile(dev DevManifest) tooldef.Package

// ValidateCompiled validates a compiled tooldef.Package against both schemas,
// returning warnings (dev mode) or errors (dist mode) as appropriate.
func ValidateCompiled(pkg tooldef.Package, mode ValidationMode, label string) ([]Warning, error)

// Inference helpers (exported so archive and source can use them if needed)
func InferEffect(entryTS string) tooldef.Effect
func InferVerb(entryTS string) string
func InferToolName(entryTS string) string
func InferToolDescription(entryTS string) string
```

### 3.2 `packaging/archive`

```go
// Pack creates a .toolbox.pkg archive from an fs.FS and a compiled tooldef.Package.
// Returns the archive bytes and the external PkgManifest (with sha256 populated).
func Pack(files fs.FS, pkg tooldef.Package) (archiveData []byte, externalManifest PkgManifest, err error)

// Unpack decompresses an archive and returns its contents as an fs.FS.
// Does not verify — call Verify first or use LoadArchive.
func Unpack(archive io.Reader) (fs.FS, error)

// Verify checks archive integrity:
// 1. Hash archive bytes → compare to externalManifest.SHA256
// 2. Extract internal toolbox.pkg.json → compare to external (ignoring sha256 field)
// Returns the verified tooldef.Package.
func Verify(archiveData []byte, externalManifest PkgManifest) (tooldef.Package, error)

// LoadArchive verifies and unpacks an archive, returning a LoadedPackage.
func LoadArchive(archiveData []byte, externalManifest PkgManifest) (packaging.LoadedPackage, error)
```

### 3.3 `packaging/source`

```go
// LoadSourceDir reads toolbox.devpkg.json from dir, validates, and compiles.
func LoadSourceDir(dir string) (tooldef.Package, error)

// LoadSourceDirWithMode loads with explicit validation mode.
func LoadSourceDirWithMode(dir string, mode manifest.ValidationMode) (manifest.LoadResult, error)

// LoadSourcePackage loads from source dir and returns LoadedPackage with filtered fs.FS.
func LoadSourcePackage(dir string) (packaging.LoadedPackage, error)

// LoadSourcePackageWithMode loads with explicit validation mode.
func LoadSourcePackageWithMode(dir string, mode manifest.ValidationMode) (packaging.LoadedPackage, error)
```

### 3.4 `packaging` (top-level)

```go
// Pack validates a source dir and produces an archive + external manifest on disk.
func Pack(dir string) (archivePath string, manifestPath string, err error)

// LoadArchive verifies and loads from archive bytes + external manifest.
func LoadArchive(archiveData []byte, externalManifest manifest.PkgManifest) (LoadedPackage, error)

// LoadDev loads from a source dir (delegates to source.LoadSourcePackage).
func LoadDev(dir string) (LoadedPackage, error)

// LoadDevWithMode loads from source dir with explicit validation mode.
func LoadDevWithMode(dir string, mode manifest.ValidationMode) (LoadedPackage, error)

// Re-exports for consumer convenience
type LoadedPackage = source.LoadedPackage  // or keep in packaging as today
type ValidationMode = manifest.ValidationMode
type Warning = manifest.Warning
type LoadResult = manifest.LoadResult
const ValidationModeDev = manifest.ValidationModeDev
const ValidationModeDist = manifest.ValidationModeDist
```

## 4. Archive format specification

### 4.1 File extension

`.toolbox.pkg` — a zstd-compressed tar archive.

### 4.2 Internal archive layout

The tar contains all files that would be in a package's `sourceFS` plus the compiled manifest:

```
toolbox.pkg.json              # compiled manifest (all fields required)
tools/
  users.list.ts
  users.get.ts
lib/
  helpers.ts                  # if matched by additionalTypeScriptGlobs
dist/
  gwc.wasm                    # WASM binaries (for wasmer runtime)
```

The archive does NOT contain `toolbox.devpkg.json`.

### 4.3 Compression

tar is piped through `zstd.NewWriter` with `SpeedDefault` level. Decompression uses `zstd.NewReader`.

### 4.4 External manifest

A standalone `toolbox.pkg.json` file is written alongside the archive:

```json
{
  "name": "google-workspace",
  "runtime": "typescript+wasmer-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    {
      "entry_ts": "tools/users.list.ts",
      "idempotent": true,
      "effect": "readOnly"
    }
  ],
  "sha256": "a1b2c3d4e5f6..."
}
```

The `sha256` field is the hex-encoded SHA-256 of the `.toolbox.pkg` file. This field is present ONLY in the external manifest, not inside the archive.

### 4.5 Verification on load

1. Read external `toolbox.pkg.json` → parse as `PkgManifest` → extract expected `sha256`
2. Compute SHA-256 of the `.toolbox.pkg` file → compare to expected
3. Decompress archive → extract internal `toolbox.pkg.json`
4. Compare internal manifest to external manifest (ignoring `sha256` field)
5. Validate the manifest with `manifest.ValidatePkg`
6. Return `LoadedPackage` with archive contents as `fs.FS`

### 4.6 Archive-backed fs.FS

`archive.Unpack` returns an in-memory `fs.FS` backed by the decompressed tar contents. This implements `fs.FS`, `fs.ReadDirFS`, and `fs.StatFS` — the same interfaces that `sourceFS` implements today. The archive FS does NOT need the filtering logic from `sourceFS` because the archive already contains only the allowed files (it was packed from a filtered source).

### 4.7 Pack flow

```
source dir + toolbox.devpkg.json
  → source.LoadSourcePackage(dir)
  → LoadedPackage { Package, Files (sourceFS), Dir }
  → archive.Pack(loadedPkg.Files, loadedPkg.Package)
  → .toolbox.pkg (tar+zstd) + toolbox.pkg.json (external, with sha256)
```

## 5. Migration path

### 5.1 What moves where

| Current location | New location | Notes |
|---|---|---|
| `packaging/packaging.go` — `packageManifest`, `packageManifestTool` | `packaging/manifest/manifest.go` — `DevManifest`, `DevTool` | Renamed to reflect devpkg semantics |
| `packaging/packaging.go` — `compilePackage` | `packaging/manifest/manifest.go` — `Compile` | Exported |
| `packaging/packaging.go` — `validateCompiledPackage` | `packaging/manifest/manifest.go` — `ValidateCompiled` | Exported |
| `packaging/packaging.go` — `mustResolveSchema` | `packaging/manifest/manifest.go` — `mustResolveSchema` | Stays unexported |
| `packaging/packaging.go` — `inferEffect`, `inferVerb`, etc. | `packaging/manifest/infer.go` | Exported |
| `packaging/packaging.go` — `ValidationMode`, `Warning`, `LoadResult` | `packaging/manifest/manifest.go` | Shared validation types |
| `packaging/packaging.go` — `LoadedPackage`, `ResolvedTools()` | `packaging/packaging.go` | Stays in top-level; `ResolvedTools` uses `manifest.InferToolName` etc. |
| `packaging/packaging.go` — `LoadSourceDir*`, `LoadSourcePackage*` | `packaging/source/source.go` | |
| `packaging/packaging.go` — `loadSourcePackageFromFile` | `packaging/source/source.go` | Unexported |
| `packaging/packaging.go` — `LoadBuiltDir*`, `LoadBuiltPackage*` | Removed | Replaced by `archive.LoadArchive` |
| `packaging/packaging.go` — `loadBuiltPackageFromFile` | Removed | No longer needed |
| `packaging/sourcefs.go` — entire file | `packaging/source/sourcefs.go` | Package changes to `source` |
| `packaging/toolbox_pkg.dev.schema.json` | `packaging/manifest/toolbox_devpkg.schema.json` | Renamed to match `toolbox.devpkg.json` |
| `packaging/toolbox_pkg.dist.schema.json` | `packaging/manifest/toolbox_pkg.schema.json` | Renamed to match `toolbox.pkg.json` |
| `packaging/effect_test.go` | `packaging/manifest/infer_test.go` | |
| `packaging/packaging_test.go` — source tests | `packaging/source/source_test.go` | |
| `packaging/packaging_test.go` — built tests | `packaging/archive/archive_test.go` | Rewritten for archive format |

### 5.2 Filename renames

| Current | New | Where |
|---|---|---|
| `toolbox.pkg.json` (source) | `toolbox.devpkg.json` | In source package dirs, fixtures, docs |
| `toolbox.pkg.compiled.json` (built) | `toolbox.pkg.json` (inside archive + external) | Archive format replaces compiled JSON |
| `toolbox_pkg.dev.schema.json` | `toolbox_devpkg.schema.json` | Schema file |
| `toolbox_pkg.dist.schema.json` | `toolbox_pkg.schema.json` | Schema file |

### 5.3 What gets deleted

- `packaging/packaging.go` — gutted to thin coordinator
- `packaging/sourcefs.go` — moved to `packaging/source/sourcefs.go`
- `packaging/toolbox_pkg.dev.schema.json` — moved/renamed
- `packaging/toolbox_pkg.dist.schema.json` — moved/renamed
- `LoadBuiltDir`, `LoadBuiltDirWithMode`, `LoadBuiltPackage`, `LoadBuiltPackageWithMode`, `loadBuiltPackageFromFile` — replaced by archive loading

## 6. `toolbox-pack` CLI design

### 6.1 Usage

```
toolbox-pack [flags] <dir>

Reads toolbox.devpkg.json from <dir>, validates, compiles, and produces:
  - <name>-<sha256>.toolbox.pkg  (zstd-compressed tar archive)
  - toolbox.pkg.json            (external manifest with sha256)

Flags:
  --output-dir string    Output directory (default: current directory)
  --verbose              Print detailed progress
```

### 6.2 Exit codes

- `0` — success
- `1` — validation error (dev manifest invalid)
- `2` — pack error (IO, compression failure)

### 6.3 Example

```bash
$ toolbox-pack ./my-tools --output-dir ./dist
Packed google-workspace-a1b2c3d4e5f6.toolbox.pkg (145.2 KB)
Wrote toolbox.pkg.json (sha256: a1b2c3d4e5f6...)
```

### 6.4 Implementation

```go
func main() {
    // Parse flags
    // LoadSourcePackage(dir) → LoadedPackage
    // archive.Pack(loadedPkg.Files, loadedPkg.Package)
    // Write <name>-<sha256>.toolbox.pkg
    // Write toolbox.pkg.json with sha256
}
```

## 7. `go.work` / `go.mod` setup

### 7.1 New module: `cmd/toolbox-pack`

```
// cmd/toolbox-pack/go.mod
module github.com/solidarity-ai/toolbox/cmd/toolbox-pack

go 1.26.1

require (
    github.com/solidarity-ai/toolbox v0.0.0
    github.com/klauspost/compress    v1.18.0
)
```

This module imports only:
- `github.com/solidarity-ai/toolbox/packaging/manifest`
- `github.com/solidarity-ai/toolbox/packaging/archive`
- `github.com/solidarity-ai/toolbox/packaging/source`
- `github.com/solidarity-ai/toolbox/tool`

It does NOT import the root module's heavy dependencies (QuickJS, wazero, esbuild, etc.) because it only depends on `packaging/` subpackages and `tool/`, which have light dependency graphs.

**However**, since `packaging/manifest`, `packaging/archive`, and `packaging/source` are subdirectories within the main module (not separate Go modules), the CLI's `go.mod` will need to depend on `github.com/solidarity-ai/toolbox` as a whole. The benefit is still realized at the binary level: the CLI binary only links the code it actually imports, so the compiled binary is small even though `go.mod` references the full module.

### 7.2 Updated `go.work`

```
go 1.26.1

use (
    .
    ./devtools
    ./cmd/toolbox-pack
)
```

### 7.3 New dependency

Add `github.com/klauspost/compress` to the main module's `go.mod` (used by `packaging/archive`).

## 8. Impact on existing consumers

### 8.1 `toolset/toolset.go`

Currently imports `packaging` and calls `packaging.LoadSourcePackage(dir)`.

**Change:** Update import and call to either:
- `packaging.LoadDev(dir)` (top-level convenience), or
- `source.LoadSourcePackage(dir)` (direct)

The `LoadedPackage` type stays the same, so `builder.Resolve()` and `ResolvedTools()` are unaffected.

### 8.2 `invoke/invoke.go`

Imports `toolset` and `tooldef`. Does not import `packaging` directly. **No changes needed.**

### 8.3 `testutil/tooltest/calc.go`

Calls `packaging.LoadSourcePackage(calcFixtureDir())`.

**Change:** Update to `packaging.LoadDev(dir)` or `source.LoadSourcePackage(dir)`.

### 8.4 Test fixtures

Rename `toolbox.pkg.json` → `toolbox.devpkg.json` in all fixture directories:
- `testutil/fixtures/toolbox.pkgs/calc/`
- `testutil/fixtures/toolbox.pkgs/google-workspace/`
- `testutil/fixtures/toolbox.pkgs/vfs-test/`

### 8.5 `service/service.go`

Does not import packaging. **No changes needed.**

### 8.6 Documentation

- `docs/architecture.md` — add `packaging/manifest`, `packaging/archive`, `packaging/source` to package overview
- `docs/pkg-tool-definition-spec.md` — update filename references (`toolbox.devpkg.json` as source, `toolbox.pkg.json` as compiled), add archive format section

## 9. Test strategy

### 9.1 `packaging/manifest`

- **Parse tests**: valid/invalid dev and pkg JSON → correct structs or errors
- **Validation tests**: dev schema allows optional fields, pkg schema requires all fields
- **Compile tests**: DevManifest with missing fields → Package with inferred values
- **Inference tests**: move existing `effect_test.go` cases, add `InferToolName`/`InferVerb` tests
- **Round-trip tests**: Compile(dev) → marshal → ParsePkg → same values

### 9.2 `packaging/archive`

- **Pack/Unpack round-trip**: pack an fs.FS → unpack → compare file contents
- **Verify tests**: correct sha256 passes, wrong sha256 fails, mismatched manifests fail
- **Archive FS tests**: unpacked archive implements fs.FS/ReadDirFS/StatFS correctly
- **LoadArchive integration**: end-to-end from archive bytes to LoadedPackage
- **Edge cases**: empty package (no tools), large WASM binaries, special characters in paths

### 9.3 `packaging/source`

- **LoadSourceDir tests**: move existing source-loading test cases from `packaging_test.go`
- **sourceFS tests**: move existing FS filtering tests
- **Validation mode tests**: dev mode warns, dist mode errors for missing fields
- **Fixture-based tests**: load calc, google-workspace, vfs-test fixtures successfully

### 9.4 `packaging` (top-level)

- **Pack integration test**: source dir → Pack → archive + manifest on disk → LoadArchive → same Package
- **LoadDev delegation**: verify it delegates correctly to source

### 9.5 `cmd/toolbox-pack`

- **CLI integration test**: run the binary on a fixture dir, verify output files exist, archive is valid
- **Error cases**: missing dir, invalid manifest, unwritable output dir

### 9.6 Test fixtures

Add a pre-packed fixture alongside existing source fixtures:
```
testutil/fixtures/toolbox.pkgs/calc/
  toolbox.devpkg.json           # source manifest (renamed)
  tools/...
testutil/fixtures/toolbox.archives/calc/
  calc-<sha256>.toolbox.pkg     # pre-packed archive
  toolbox.pkg.json              # external manifest with sha256
```

## 10. Open questions

1. **~~Should `LoadedPackage` stay in the top-level `packaging` or move to a shared internal package?~~ Resolved: `LoadedPackage` differs enough from `tool.Package` to stay its own type, placed in a shared internal package.**
   `LoadedPackage` has `Files fs.FS`, `Dir string`, and `Executables map[string]string` beyond what `tool.Package` carries — it can't be a simple type alias. Since both `source` and `archive` need to return it while `packaging` top-level imports both of them, we'll put `LoadedPackage` in `packaging/internal/pkgtype` (or similar) to avoid circular imports. Subpackages import the internal type; the top-level re-exports it.

2. **~~Should `ResolvedTools()` move off `LoadedPackage`?~~ Resolved: keep `Executables` on `LoadedPackage`, not on `tooldef.Package`.**
   Currently `LoadedPackage.ResolvedTools()` re-reads `toolbox.pkg.json` from disk to get the executables map for `TSWasmToolDef`. With the archive format there's no source dir to re-read from. The fix: add an `Executables map[string]string` field to `LoadedPackage`. Both the source loader and archive loader populate it from the manifest during loading. `ResolvedTools()` then uses `p.Executables` instead of re-reading from disk, and the data still ends up on `TSWasmToolDef` where it belongs. `tooldef.Package` stays clean as the static, runtime-agnostic definition — executables are a runtime-specific concern that belongs on the loaded/resolved side.

3. **~~Archive file naming: `<name>.toolbox.pkg` or flat `package.toolbox.pkg`?~~ Resolved: `<name>-<sha256>.toolbox.pkg`.**
   Embedding the sha256 in the filename makes archives content-addressable and avoids collisions when multiple versions exist in the same directory. The external `toolbox.pkg.json` still contains the `sha256` field for programmatic verification.

4. **~~Should `packaging/archive` include WASM binaries from `executables` in the archive?~~ Resolved: yes, delegate file collection to runtime-specific logic.**
   The archive must be self-contained. Different runtimes need different files packed (TS-only packages need just TS sources, wasmer packages also need WASM binaries). Rather than hardcoding this in `archive.Pack`, delegate file collection to runtime-aware objects so each runtime type defines which files belong in the archive. The pack step collects tool entries + additional globs for all runtimes, and additionally includes executable paths for wasmer runtimes.

5. **~~Schema file naming in the repo vs manifest filenames.~~ Resolved: confirmed.**
   `toolbox_devpkg.schema.json` validates `toolbox.devpkg.json`. `toolbox_pkg.schema.json` validates `toolbox.pkg.json`.

6. **~~Should the `toolbox-pack` CLI live at `packaging/cmd/toolbox-pack` or at the repo root `cmd/toolbox-pack`?~~ Resolved: `cmd/toolbox-pack`.**
   Follows standard Go convention. Still gets its own `go.mod` for dependency isolation.

7. **~~Backward compatibility period.~~ Resolved: no backwards compat — clean rename in one shot.**
   Update all test fixtures in `testutil/fixtures/toolbox.pkgs/` and the `/tmp/google-workspace` testbed directory. No transition period needed since this is pre-1.0.
