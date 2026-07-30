package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	"github.com/solidarity-ai/toolbox/packaging/internal/source"
	"github.com/solidarity-ai/toolbox/registry/testutil/gitfixture"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const testPackerVersion = tooldef.Version("v1.0.0")

func TestPack(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	for _, version := range []tooldef.Version{"", "v0.0.0-20260327112233-abcdef123456", "v01.0.0"} {
		if _, err := Pack(loaded, outDir, version); err == nil {
			t.Fatalf("Pack() accepted non-release packer version %q", version)
		}
	}
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	// Archive file should exist
	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Fatalf("archive file not found: %v", err)
	}

	// External manifest should exist
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Fatalf("external manifest not found: %v", err)
	}

	// Archive should have .toolbox.pkg extension
	if filepath.Ext(result.ArchivePath) != ".pkg" {
		if !strings.HasSuffix(result.ArchivePath, ".toolbox.pkg") {
			t.Fatalf("expected .toolbox.pkg extension, got %q", result.ArchivePath)
		}
	}

	// External manifest should have sha256
	raw, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read external manifest: %v", err)
	}
	pkg, err := manifest.ParsePkg(raw)
	if err != nil {
		t.Fatalf("parse external manifest: %v", err)
	}
	if pkg.SHA256 == "" {
		t.Fatalf("expected sha256 in external manifest")
	}
	if pkg.ManifestSchemaVersion != tooldef.PackageManifestSchemaVersion ||
		pkg.MinimumToolboxVersion != testPackerVersion ||
		pkg.PackedByToolboxVersion != testPackerVersion {
		t.Fatalf("pack metadata = (%d, %s, %s), want (%d, %s, %s)",
			pkg.ManifestSchemaVersion, pkg.MinimumToolboxVersion, pkg.PackedByToolboxVersion,
			tooldef.PackageManifestSchemaVersion, testPackerVersion, testPackerVersion)
	}

	// Verify sha256 matches actual archive
	archiveData, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	h := sha256.Sum256(archiveData)
	expectedHash := hex.EncodeToString(h[:])
	if pkg.SHA256 != expectedHash {
		t.Fatalf("sha256 mismatch: manifest=%q, actual=%q", pkg.SHA256, expectedHash)
	}
}

func TestPackRoundTrip(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	// Load the archive back
	archiveLoaded, err := LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err != nil {
		t.Fatalf("LoadArchive() error: %v", err)
	}

	// Package metadata should match
	if loaded.Package.Name != archiveLoaded.Package.Name {
		t.Fatalf("name mismatch: want %q, got %q", loaded.Package.Name, archiveLoaded.Package.Name)
	}
	if loaded.Package.Runtime != archiveLoaded.Package.Runtime {
		t.Fatalf("runtime mismatch: want %q, got %q", loaded.Package.Runtime, archiveLoaded.Package.Runtime)
	}
	if len(archiveLoaded.Package.Tools) != len(loaded.Package.Tools) {
		t.Fatalf("tools count mismatch: want %d, got %d", len(loaded.Package.Tools), len(archiveLoaded.Package.Tools))
	}

	// Files from the archive should be readable
	content, err := fs.ReadFile(archiveLoaded.Files, "tools/calc.add.ts")
	if err != nil {
		t.Fatalf("read tool from archive: %v", err)
	}
	if !strings.Contains(string(content), "function") {
		t.Fatalf("expected tool content, got %q", string(content))
	}
}

func TestLoadArchiveVerifiesSHA256(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	// Tamper with the archive
	archiveData, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	archiveData[len(archiveData)-1] ^= 0xFF
	if err := os.WriteFile(result.ArchivePath, archiveData, 0o644); err != nil {
		t.Fatalf("write tampered archive: %v", err)
	}

	_, err = LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err == nil {
		t.Fatalf("expected sha256 verification error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected sha256 error, got: %v", err)
	}
}

func TestLoadArchiveRequiresSHA256(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	raw, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	pkg, err := manifest.ParsePkg(raw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	pkg.SHA256 = ""
	writePkgManifest(t, result.ManifestPath, pkg)

	_, err = LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err == nil {
		t.Fatalf("expected missing sha256 error")
	}
	if !strings.Contains(err.Error(), "missing sha256") {
		t.Fatalf("expected missing sha256 error, got: %v", err)
	}
}

func TestLoadArchiveVerifiesManifestMatch(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	// Modify external manifest name to mismatch
	raw, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	tampered := strings.Replace(string(raw), `"calc"`, `"tampered"`, 1)
	if err := os.WriteFile(result.ManifestPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}

	_, err = LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err == nil {
		t.Fatalf("expected manifest mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected mismatch error, got: %v", err)
	}
}

func TestLoadArchiveAcceptsMissingIdempotent(t *testing.T) {
	t.Parallel()

	dir := setupTestPackageWithoutIdempotent(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	_, err = LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err != nil {
		t.Fatalf("expected no dist validation error for missing idempotent, got: %v", err)
	}
}

func TestArchiveContainsInternalManifest(t *testing.T) {
	t.Parallel()

	dir := setupTestPackage(t)
	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	archiveLoaded, err := LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err != nil {
		t.Fatalf("LoadArchive() error: %v", err)
	}

	// Internal manifest should be readable as a file
	internalManifest, err := fs.ReadFile(archiveLoaded.Files, manifest.PkgManifestFilename)
	if err != nil {
		t.Fatalf("read internal manifest from archive: %v", err)
	}

	internalPkg, err := manifest.ParsePkg(internalManifest)
	if err != nil {
		t.Fatalf("parse internal manifest: %v", err)
	}
	if internalPkg.Name != "calc" {
		t.Fatalf("expected internal manifest name=calc, got %q", internalPkg.Name)
	}
}

func TestPackBundlesExecutables(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "module": "example.com/wasm-pkg",
  "name": "wasm-pkg",
  "runtime": "typescript+wasix-sandbox",
  "executables": { "guest": "dist/guest.wasm" },
  "tools": [
    { "entry_ts": "tools/run.ts", "idempotent": true, "effect": "irreversible" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "run.ts"), `export default function tool() { return "ok"; }`)
	mustWriteFile(t, filepath.Join(dir, "dist", "guest.wasm"), "fake-wasm-binary")

	loaded, err := source.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	outDir := t.TempDir()
	result, err := Pack(loaded, outDir, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	archiveLoaded, err := LoadArchive(result.ArchivePath, result.ManifestPath, nil)
	if err != nil {
		t.Fatalf("LoadArchive() error: %v", err)
	}

	// The WASM executable must be readable from the archive
	content, err := fs.ReadFile(archiveLoaded.Files, "dist/guest.wasm")
	if err != nil {
		t.Fatalf("executable not found in archive: %v", err)
	}
	if string(content) != "fake-wasm-binary" {
		t.Fatalf("executable content mismatch: got %q", string(content))
	}

	// The executables map must be present in the package
	if archiveLoaded.Package.Executables == nil {
		t.Fatalf("expected executables in loaded package")
	}
	if archiveLoaded.Package.Executables["guest"] != "dist/guest.wasm" {
		t.Fatalf("expected executable guest=dist/guest.wasm, got %q", archiveLoaded.Package.Executables["guest"])
	}
}

func TestPackSameCommittedPackageTwiceProducesIdenticalArchive(t *testing.T) {
	t.Parallel()

	srcDir := setupTestPackage(t)
	repoDir := gitfixture.CreateTaggedRepoFromDir(t, "v1.0.0", srcDir)

	clone1 := filepath.Join(t.TempDir(), "clone1")
	runGitClone(t, repoDir, clone1)
	time.Sleep(2 * time.Second)
	clone2 := filepath.Join(t.TempDir(), "clone2")
	runGitClone(t, repoDir, clone2)

	loaded1, err := source.LoadDir(clone1)
	if err != nil {
		t.Fatalf("LoadDir(clone1): %v", err)
	}
	loaded2, err := source.LoadDir(clone2)
	if err != nil {
		t.Fatalf("LoadDir(clone2): %v", err)
	}

	out1 := t.TempDir()
	result1, err := Pack(loaded1, out1, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack(clone1): %v", err)
	}
	out2 := t.TempDir()
	result2, err := Pack(loaded2, out2, testPackerVersion)
	if err != nil {
		t.Fatalf("Pack(clone2): %v", err)
	}

	archive1, err := os.ReadFile(result1.ArchivePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", result1.ArchivePath, err)
	}
	archive2, err := os.ReadFile(result2.ArchivePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", result2.ArchivePath, err)
	}

	hash1 := sha256.Sum256(archive1)
	hash2 := sha256.Sum256(archive2)
	if hash1 != hash2 {
		t.Fatalf("same committed package produced different archive hashes: clone1=%s clone2=%s", hex.EncodeToString(hash1[:]), hex.EncodeToString(hash2[:]))
	}
}

func setupTestPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "idempotent": true, "effect": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), `export default function tool() { return "ok"; }`)
	return dir
}

func setupTestPackageWithoutIdempotent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifest.DevManifestFilename), `{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/calc.add.ts", "effect": "readOnly" }
  ]
}`)
	mustWriteFile(t, filepath.Join(dir, "tools", "calc.add.ts"), `export default function tool() { return "ok"; }`)
	return dir
}

func runGitClone(t *testing.T, repoDir string, cloneDir string) {
	t.Helper()
	cmd := exec.Command("git", "clone", fmt.Sprintf("file://%s", repoDir), cloneDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone %s %s failed: %v\n%s", repoDir, cloneDir, err, strings.TrimSpace(string(output)))
	}
}

func writePkgManifest(t *testing.T, path string, pkg any) {
	t.Helper()
	raw, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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
