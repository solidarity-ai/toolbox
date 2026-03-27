package packaging_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestLoadDev(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), "export default function() {}")

	loaded, err := packaging.LoadDev(dir)
	if err != nil {
		t.Fatalf("LoadDev() error: %v", err)
	}
	if loaded.Package.Name != "calc" {
		t.Fatalf("expected name=calc, got %q", loaded.Package.Name)
	}
	if loaded.Package.Runtime != tooldef.RuntimeTypeScriptSandbox {
		t.Fatalf("expected runtime=typescript-sandbox, got %q", loaded.Package.Runtime)
	}
}

func TestPackAndLoadArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), `export default function tool() { return "ok"; }`)

	outDir := t.TempDir()
	result, err := packaging.Pack(dir, outDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	loaded, err := packaging.LoadArchive(result.ArchivePath, result.ManifestPath)
	if err != nil {
		t.Fatalf("LoadArchive() error: %v", err)
	}
	if loaded.Package.Name != "calc" {
		t.Fatalf("expected name=calc, got %q", loaded.Package.Name)
	}
}

func TestPackAcceptsMissingIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), `export default function tool() { return "ok"; }`)

	outDir := t.TempDir()
	_, err := packaging.Pack(dir, outDir)
	if err != nil {
		t.Fatalf("expected no error for missing idempotent, got: %v", err)
	}
}

func TestPackRoundTripAllFixtures(t *testing.T) {
	t.Parallel()

	for _, dir := range fixtures.SourceDirs() {
		dir := dir
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loaded, err := packaging.LoadDev(dir)
			if err != nil {
				t.Fatalf("LoadDev(%s): %v", dir, err)
			}

			outDir := t.TempDir()
			result, err := packaging.Pack(dir, outDir)
			if err != nil {
				t.Fatalf("Pack() error: %v", err)
			}

			archiveLoaded, err := packaging.LoadArchive(result.ArchivePath, result.ManifestPath)
			if err != nil {
				t.Fatalf("LoadArchive() error: %v", err)
			}

			if diff := cmp.Diff(loaded.Package.Name, archiveLoaded.Package.Name); diff != "" {
				t.Fatalf("name mismatch: %s", diff)
			}
			if diff := cmp.Diff(loaded.Package.Runtime, archiveLoaded.Package.Runtime); diff != "" {
				t.Fatalf("runtime mismatch: %s", diff)
			}
			if len(archiveLoaded.Package.Tools) != len(loaded.Package.Tools) {
				t.Fatalf("tools count mismatch: want %d, got %d",
					len(loaded.Package.Tools), len(archiveLoaded.Package.Tools))
			}

			for _, tool := range loaded.Package.Tools {
				if _, err := fs.ReadFile(archiveLoaded.Files, tool.EntryTS); err != nil {
					t.Errorf("tool %s not readable from archive: %v", tool.EntryTS, err)
				}
			}
		})
	}
}

func TestLoadDevWasip2Runtime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "name": "http-client",
  "runtime": "typescript+wasip2-sandbox",
  "executables": { "http-client": "dist/http-client.wasm" },
  "tools": [
    { "entry_ts": "tools/http-client.fetch.ts", "idempotent": true, "accessMode": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "http-client.fetch.ts"), "export default function() {}")

	loaded, err := packaging.LoadDev(dir)
	if err != nil {
		t.Fatalf("LoadDev() error: %v", err)
	}
	if loaded.Package.Runtime != tooldef.RuntimeTypeScriptWasip2Sandbox {
		t.Fatalf("expected runtime=typescript+wasip2-sandbox, got %q", loaded.Package.Runtime)
	}
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
