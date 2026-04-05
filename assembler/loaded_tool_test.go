package assembler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
)

func TestLoadedTools(t *testing.T) {
	t.Parallel()

	t.Run("typescript sandbox tools", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" },
    { "entry_ts": "tools/calc.sub.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
		mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), "export default function() {}")
		mustWriteFile(t, filepath.Join(dir, "tools", "calc.sub.ts"), "export default function() {}")

		loaded, err := packaging.LoadDev(dir)
		if err != nil {
			t.Fatalf("LoadDev() error: %v", err)
		}

		tools := LoadedTools(loaded)
		if len(tools) != 2 {
			t.Fatalf("expected 2 loaded tools, got %d", len(tools))
		}
		if tools[0].Name != "calc.add" {
			t.Fatalf("expected first tool name calc.add, got %q", tools[0].Name)
		}
		if tools[0].TS == nil {
			t.Fatalf("expected TS definition for calc.add")
		}
		if tools[0].TSWasm != nil {
			t.Fatalf("expected no TSWasm for typescript-sandbox tool")
		}
	})

	t.Run("wasix sandbox tools", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, packaging.DevManifestFilename), `{
  "module": "example.com/google-workspace",
  "name": "google-workspace",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "gwc": "dist/gwc.wasm" },
  "tools": [
    { "entry_ts": "tools/users.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
		mustWriteFile(t, filepath.Join(dir, "tools", "users.list.ts"), "export default function() {}")

		loaded, err := packaging.LoadDev(dir)
		if err != nil {
			t.Fatalf("LoadDev() error: %v", err)
		}

		tools := LoadedTools(loaded)
		if len(tools) != 1 {
			t.Fatalf("expected 1 loaded tool, got %d", len(tools))
		}
		if tools[0].TSWasm == nil {
			t.Fatalf("expected TSWasm definition for wasix tool")
		}
		if tools[0].TSWasm.Executables["gwc"] != "dist/gwc.wasm" {
			t.Fatalf("expected executable gwc=dist/gwc.wasm, got %q", tools[0].TSWasm.Executables["gwc"])
		}
	})
}

func mustWriteFile(t testing.TB, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
