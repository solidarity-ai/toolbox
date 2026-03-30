package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
)

func TestCache(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	module := ModulePath("example.com/acme/calc")
	version := Version("v1.2.3")

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "PutHasRoundTrip",
			run: func(t *testing.T) {
				cache := newTempCache(t)
				if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
					t.Fatalf("Put() error: %v", err)
				}
				if !cache.Has(module, version) {
					t.Fatalf("Has() = false, want true")
				}
			},
		},
		{
			name: "HasMissingEntry",
			run: func(t *testing.T) {
				cache := newTempCache(t)
				if cache.Has(module, version) {
					t.Fatalf("Has() = true, want false")
				}
			},
		},
		{
			name: "PutLoadArchiveRoundTrip",
			run: func(t *testing.T) {
				cache := newTempCache(t)
				if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
					t.Fatalf("Put() error: %v", err)
				}

				loaded, err := cache.LoadArchive(module, version)
				if err != nil {
					t.Fatalf("LoadArchive() error: %v", err)
				}
				if loaded.Package.Name != "calc" {
					t.Fatalf("loaded package name = %q, want %q", loaded.Package.Name, "calc")
				}
			},
		},
		{
			name: "PathStructureVerification",
			run: func(t *testing.T) {
				root := t.TempDir()
				cache, err := NewCache(root)
				if err != nil {
					t.Fatalf("NewCache() error: %v", err)
				}
				if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
					t.Fatalf("Put() error: %v", err)
				}

				paths := cache.paths(module, version)
				for _, path := range []string{paths.archive, paths.manifest, paths.info} {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("expected cached file at %s: %v", path, err)
					}
				}
				wantDir := filepath.Join(root, module.String(), "@v")
				if filepath.Dir(paths.archive) != wantDir {
					t.Fatalf("archive dir = %q, want %q", filepath.Dir(paths.archive), wantDir)
				}

				raw, err := os.ReadFile(paths.info)
				if err != nil {
					t.Fatalf("read info file: %v", err)
				}
				var info struct {
					Version string `json:"version"`
				}
				if err := json.Unmarshal(raw, &info); err != nil {
					t.Fatalf("unmarshal info file: %v", err)
				}
				if info.Version != version.String() {
					t.Fatalf("info version = %q, want %q", info.Version, version.String())
				}
			},
		},
		{
			name: "ToolboxCacheDirOverride",
			run: func(t *testing.T) {
				override := t.TempDir()
				t.Setenv(cacheDirEnv, override)

				cache, err := NewCache("")
				if err != nil {
					t.Fatalf("NewCache() error: %v", err)
				}
				if cache.root != override {
					t.Fatalf("cache root = %q, want %q", cache.root, override)
				}
			},
		},
		{
			name: "LoadArchiveMissingEntryReturnsError",
			run: func(t *testing.T) {
				cache := newTempCache(t)
				if _, err := cache.LoadArchive(module, version); err == nil {
					t.Fatalf("LoadArchive() error = nil, want non-nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

func TestCachePreservesModuleIdentityInArchiveLoads(t *testing.T) {
	archiveBytes, manifestBytes := buildPackageArchiveFixture(t, `{
  "module": "github.com/example/github-tools",
  "name": "github-tools",
  "runtime": "typescript-sandbox",
  "credentials": [
    {
      "name": "github_token",
      "type": "bearer",
      "inject": { "hosts": ["api.github.com"], "method": "bearer_header" }
    }
  ],
  "tools": [
    { "entry_ts": "tools/issues.list.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
	cache := newTempCache(t)
	module := ModulePath("github.com/example/github-tools")
	version := Version("v1.2.3")
	if err := cache.Put(module, version, archiveBytes, manifestBytes); err != nil {
		t.Fatalf("Put() error: %v", err)
	}

	loaded, err := cache.LoadArchive(module, version)
	if err != nil {
		t.Fatalf("LoadArchive() error: %v", err)
	}
	if got := loaded.Package.Module; got != module {
		t.Fatalf("loaded module = %q, want %q", got, module)
	}
	if len(loaded.Package.Credentials) != 1 {
		t.Fatalf("loaded credentials = %d, want 1", len(loaded.Package.Credentials))
	}
}

func newTempCache(t *testing.T) *Cache {
	t.Helper()
	cache, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache() error: %v", err)
	}
	return cache
}

func loadDistFixtureBytes(t *testing.T, fixtureName string) ([]byte, []byte) {
	t.Helper()

	fixtureDir := ""
	for _, dir := range fixtures.DistDirs() {
		if filepath.Base(dir) == fixtureName {
			fixtureDir = dir
			break
		}
	}
	if fixtureDir == "" {
		t.Fatalf("dist fixture %q not found", fixtureName)
	}

	archivePath := filepath.Join(fixtureDir, "calc.toolbox.pkg")
	manifestPath := filepath.Join(fixtureDir, packaging.PkgManifestFilename)

	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive fixture %s: %v", archivePath, err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", manifestPath, err)
	}
	return archiveBytes, manifestBytes
}

func buildPackageArchiveFixture(t *testing.T, manifestJSON string) ([]byte, []byte) {
	t.Helper()

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, packaging.DevManifestFilename), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "tools"), 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "tools", "issues.list.ts"), []byte("export default function() { return 'ok'; }\n"), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	outDir := t.TempDir()
	result, err := packaging.Pack(srcDir, outDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	archiveBytes, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	manifestBytes, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	return archiveBytes, manifestBytes
}
