package toolsetfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestToolsetFileLoad(t *testing.T) {
	t.Parallel()

	t.Run("ValidFileWithMultipleEntriesPreservesDeclaredTools", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc":       "v1.2.3",
				"example.com/solidarity/echo": "v2.0.0",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
				{"tool": "example.com/solidarity/echo@v2.0.0/echo.say"},
			},
		})

		got, err := Load(filename)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		wantPackages := map[string]string{
			"example.com/acme/calc":       "v1.2.3",
			"example.com/solidarity/echo": "v2.0.0",
		}
		if !reflect.DeepEqual(got.Packages, wantPackages) {
			t.Fatalf("Packages = %#v, want %#v", got.Packages, wantPackages)
		}

		gotTools := []string{got.Tools[0].Tool, got.Tools[1].Tool}
		wantTools := []string{
			"example.com/acme/calc@v1.2.3/calc.add",
			"example.com/solidarity/echo@v2.0.0/echo.say",
		}
		if !reflect.DeepEqual(gotTools, wantTools) {
			t.Fatalf("tool order = %#v, want %#v", gotTools, wantTools)
		}
	})

	t.Run("StoresSourceAndDerivedLockFilename", func(t *testing.T) {
		filename := writeToolsetJSONNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})

		got, err := Load(filename)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if got.SourceFilename() != filename {
			t.Fatalf("SourceFilename() = %q, want %q", got.SourceFilename(), filename)
		}
		wantLock := strings.TrimSuffix(filename, ".json") + ".lock"
		if got.LockFilename() != wantLock {
			t.Fatalf("LockFilename() = %q, want %q", got.LockFilename(), wantLock)
		}
		wantLocal := strings.TrimSuffix(filename, toolsetFilenameSuffix) + toolsetLocalFilenameSuffix
		if got.LocalFilename() != wantLocal {
			t.Fatalf("LocalFilename() = %q, want %q", got.LocalFilename(), wantLocal)
		}
	})

	t.Run("MissingFileReturnsReadErrorWithFilename", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "toolbox.toolset.json")

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want read error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		if !strings.Contains(err.Error(), filename) {
			t.Fatalf("error = %q, want filename %q", err.Error(), filename)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("InvalidJSONReturnsParseError", func(t *testing.T) {
		filename := filepath.Join(t.TempDir(), "toolbox.toolset.json")
		if err := os.WriteFile(filename, []byte("{bad"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", filename, err)
		}

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want parse error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "parse toolset file")
		assertErrorContains(t, err, filename)
	})

	t.Run("SchemaRejectsUnknownTopLevelField", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
			"unexpected": true,
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "validate toolset file")
		assertErrorContains(t, err, "unexpected")
	})

	t.Run("SchemaRejectsWrongPackagesShape", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": []string{"example.com/acme/calc@v1.2.3"},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want schema validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "validate toolset file")
		assertErrorContains(t, err, "packages")
	})

	t.Run("EmptyToolsetAllowed", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{},
			"tools":    []map[string]string{},
		})

		got, err := Load(filename)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if len(got.Packages) != 0 {
			t.Fatalf("len(Packages) = %d, want 0", len(got.Packages))
		}
		if len(got.Tools) != 0 {
			t.Fatalf("len(Tools) = %d, want 0", len(got.Tools))
		}
	})

	t.Run("EmptyToolsAllowed", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{},
		})

		got, err := Load(filename)
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if len(got.Tools) != 0 {
			t.Fatalf("len(Tools) = %d, want 0", len(got.Tools))
		}
	})

	t.Run("InvalidModulePathRejected", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"bad": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, `packages["bad"]`)
		assertErrorContains(t, err, "module path")
	})

	t.Run("InvalidVersionRejected", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "notaversion",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, `packages["example.com/acme/calc"]`)
		assertErrorContains(t, err, "invalid version")
	})

	t.Run("InvalidToolFQNRejected", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "not-a-fqn"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "tools[0].tool")
		assertErrorContains(t, err, "tool FQN")
	})

	t.Run("ToolReferencesUndeclaredPackageRejected", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/echo@v1.2.3/echo.say"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "tools[0].tool")
		assertErrorContains(t, err, "is not declared in packages")
	})

	t.Run("ToolVersionMismatchRejected", func(t *testing.T) {
		filename := writeToolsetJSON(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.4/calc.add"},
			},
		})

		got, err := Load(filename)
		if err == nil {
			t.Fatal("Load() error = nil, want validation error")
		}
		if got != nil {
			t.Fatalf("Load() toolset = %#v, want nil", got)
		}
		assertErrorContains(t, err, "tools[0].tool")
		assertErrorContains(t, err, "does not match declared package version")
	})
}

func TestToolsetFilePrepare(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("NilToolsetRejected", func(t *testing.T) {
		var file *ToolsetFile

		got, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t)))
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, "nil toolset file")
	})

	t.Run("UnvalidatedToolsetRejected", func(t *testing.T) {
		file := &ToolsetFile{Packages: map[string]string{}, Tools: []ToolEntry{}}

		got, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t)))
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, "must be loaded and validated before prepare")
	})

	t.Run("NilResolverReturnsErrNoResolver", func(t *testing.T) {
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})
		beforeTools := toolEntryStrings(file.Tools)

		got, err := file.Prepare(ctx, nil)
		if err == nil {
			t.Fatal("Prepare() error = nil, want ErrNoResolver")
		}
		if !errors.Is(err, assembler.ErrNoResolver) {
			t.Fatalf("error = %v, want ErrNoResolver", err)
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		if !reflect.DeepEqual(toolEntryStrings(file.Tools), beforeTools) {
			t.Fatalf("declared tools changed after Prepare error: got %#v, want %#v", toolEntryStrings(file.Tools), beforeTools)
		}
	})

	t.Run("ReplaceOverlayResolvesFromLocalDirPreservesExistingLockEntryAndSkipsRegistryFetch", func(t *testing.T) {
		githubIssuesDir, err := filepath.Abs(filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "github-issues"))
		if err != nil {
			t.Fatalf("filepath.Abs(github-issues fixture): %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"fixtures.local/github-issues": "v2.0.0",
				"fixtures.local/calc":          "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "fixtures.local/github-issues@v2.0.0/github-issues.get"},
				{"tool": "fixtures.local/calc@v1.2.3/calc.add"},
			},
		})
		writeToolsetLocalJSON(t, file.LocalFilename(), map[string]any{
			"replace": map[string]string{
				"fixtures.local/github-issues": githubIssuesDir,
			},
		})

		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath("fixtures.local/calc"), registry.Version("v1.2.3"), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		resolver := registry.NewResolver(cache, &recordingSource{err: fmt.Errorf("unexpected fetch for local replacement test")})

		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"fixtures.local/github-issues@v2.0.0": {
				ArchiveSHA256: strings.Repeat("a", 64),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T12:05:00Z",
			},
			"fixtures.local/calc@v1.2.3": {
				ArchiveSHA256: sha256HexForTest(archiveBytes),
				GitSHA:        strings.Repeat("c", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitSource,
				ResolvedAt:    "2026-03-28T12:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}

		got, err := file.Prepare(ctx, resolver)
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}

		gotIDs := preparedToolIDs(got)
		wantIDs := []string{"calc/calc.add", "calc/calc.sub", "calc/calc.asyncAdd", "github-issues/githubIssues.get"}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("prepared tools = %#v, want %#v", gotIDs, wantIDs)
		}
		if orderedPackages := preparedPackageNames(got); !reflect.DeepEqual(orderedPackages, []string{"calc", "calc", "calc", "github-issues"}) {
			t.Fatalf("prepared package order = %#v, want %#v", orderedPackages, []string{"calc", "calc", "calc", "github-issues"})
		}

		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed during mixed local+registry prepare:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})

	t.Run("ReplaceOverlayDoesNotCreateNewLockEntryForReplacedOnlyPackage", func(t *testing.T) {
		githubIssuesDir, err := filepath.Abs(filepath.Join("..", "testutil", "fixtures", "toolbox.pkgs", "github-issues"))
		if err != nil {
			t.Fatalf("filepath.Abs(github-issues fixture): %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"fixtures.local/github-issues": "v2.0.0",
			},
			"tools": []map[string]string{{
				"tool": "fixtures.local/github-issues@v2.0.0/github-issues.get",
			}},
		})
		writeToolsetLocalJSON(t, file.LocalFilename(), map[string]any{
			"replace": map[string]string{
				"fixtures.local/github-issues": githubIssuesDir,
			},
		})

		got, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t), &recordingSource{err: fmt.Errorf("unexpected fetch for replaced-only package")}))
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}
		if len(got.Tools()) == 0 {
			t.Fatal("Prepare() returned no tools")
		}

		lock, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		if len(lock.Packages) != 0 {
			t.Fatalf("lock packages = %#v, want none for replaced-only package", lock.Packages)
		}
	})

	t.Run("MalformedLocalOverlayAbortsBeforeAnyResolutionAndLeavesLockfileUntouched", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		if err := os.WriteFile(file.LocalFilename(), []byte(`{"replace":["bad"]}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", file.LocalFilename(), err)
		}
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/calc@v1.2.3": {
				ArchiveSHA256: strings.Repeat("a", 64),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{err: fmt.Errorf("should not fetch when local overlay is malformed")}

		_, err = file.Prepare(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want local overlay error")
		}
		assertErrorContains(t, err, "prepare toolset file")
		assertErrorContains(t, err, "schema-validate toolset local file")
		assertErrorContains(t, err, file.LocalFilename())
		if len(src.calls) != 0 {
			t.Fatalf("source calls = %#v, want none", src.calls)
		}
		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed on malformed local overlay:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})

	t.Run("BadReplacePathFailsBeforeLaterSortedPackagesAndLeavesLockfileUntouched", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"example.com/zeta/never-reached": "v9.9.9",
				"example.com/acme/first":         "v1.0.0",
			},
			"tools": []map[string]string{},
		})
		writeToolsetLocalJSON(t, file.LocalFilename(), map[string]any{
			"replace": map[string]string{
				"example.com/acme/first": "./missing-package-dir",
			},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/first@v1.0.0": {
				ArchiveSHA256: strings.Repeat("a", 64),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{err: fmt.Errorf("should not fetch later sorted package")}

		_, err = file.Prepare(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want replace path error")
		}
		assertErrorContains(t, err, "resolve example.com/acme/first@v1.0.0 from local replace")
		assertErrorContains(t, err, filepath.Clean(filepath.Join(filepath.Dir(file.LocalFilename()), "missing-package-dir")))
		assertErrorContains(t, err, "toolbox.devpkg.json")
		if len(src.calls) != 0 {
			t.Fatalf("source calls = %#v, want none", src.calls)
		}
		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed on replace failure:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})

	t.Run("ReplacePathToNonPackageDirectoryFailsWithContextAndLeavesLockfileUntouched", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		nonPackageDir := filepath.Join(t.TempDir(), "not-a-package")
		if err := os.MkdirAll(nonPackageDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", nonPackageDir, err)
		}
		writeToolsetLocalJSON(t, file.LocalFilename(), map[string]any{
			"replace": map[string]string{
				"example.com/acme/calc": nonPackageDir,
			},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/calc@v1.2.3": {
				ArchiveSHA256: strings.Repeat("a", 64),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}

		_, err = file.Prepare(ctx, registry.NewResolver(newTempCache(t)))
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-package directory error")
		}
		assertErrorContains(t, err, "resolve example.com/acme/calc@v1.2.3 from local replace")
		assertErrorContains(t, err, nonPackageDir)
		assertErrorContains(t, err, "toolbox.devpkg.json")
		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed on non-package replace failure:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})

	t.Run("DuplicateLoadedPackageNamesAreRejectedWithoutAliases", func(t *testing.T) {
		firstDir := filepath.Join(t.TempDir(), "first-calc")
		secondDir := filepath.Join(t.TempDir(), "second-calc")
		if err := os.MkdirAll(firstDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", firstDir, err)
		}
		if err := os.MkdirAll(secondDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", secondDir, err)
		}

		copyFixtureDir(t, loadSourceFixtureDir(t, "calc"), firstDir)
		copyFixtureDir(t, loadSourceFixtureDir(t, "calc"), secondDir)

		const firstModule = "example.com/acme/calc-one"
		const secondModule = "example.com/other/calc-two"
		rewriteSourceFixtureModule(t, firstDir, firstModule)
		rewriteSourceFixtureModule(t, secondDir, secondModule)

		file := mustLoadToolsetFileNamed(t, "duplicate-names.toolset.json", map[string]any{
			"packages": map[string]string{
				firstModule:  "v1.2.3",
				secondModule: "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": firstModule + "@v1.2.3/calc.add"},
				{"tool": secondModule + "@v1.2.3/calc.add"},
			},
		})
		writeToolsetLocalJSON(t, file.LocalFilename(), map[string]any{
			"replace": map[string]string{
				firstModule:  firstDir,
				secondModule: secondDir,
			},
		})

		got, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t), &recordingSource{
			err: fmt.Errorf("unexpected fetch for duplicate-name local replacements"),
		}))
		if err == nil {
			t.Fatal("Prepare() error = nil, want duplicate package name error")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, `package name "calc"`)
		assertErrorContains(t, err, firstModule)
		assertErrorContains(t, err, secondModule)
		assertErrorContains(t, err, "require aliases")
	})

	t.Run("DuplicateLoadedPackageNamesAreAllowedWhenAliased", func(t *testing.T) {
		t.Skip("toolset aliases are not implemented yet")
	})

	t.Run("PreparesFromPrePopulatedCacheMatchesImperativeBuilderInSortedPackageOrder", func(t *testing.T) {
		fixtures := []struct {
			module  string
			version string
			fixture string
		}{
			{module: "fixtures.local/github-issues", version: "v2.0.0", fixture: "github-issues-dist"},
			{module: "fixtures.local/calc", version: "v1.2.3", fixture: "calc-dist"},
		}

		cache := newTempCache(t)
		for _, fixture := range fixtures {
			archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, fixture.fixture)
			if err := cache.Put(registry.ModulePath(fixture.module), registry.Version(fixture.version), archiveBytes, manifestBytes); err != nil {
				t.Fatalf("seed cache for %s@%s: %v", fixture.module, fixture.version, err)
			}
		}

		resolver := registry.NewResolver(cache)
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				fixtures[0].module: fixtures[0].version,
				fixtures[1].module: fixtures[1].version,
			},
			"tools": []map[string]string{
				{"tool": fmt.Sprintf("%s@%s/github-issues.get", fixtures[0].module, fixtures[0].version)},
				{"tool": fmt.Sprintf("%s@%s/calc.add", fixtures[1].module, fixtures[1].version)},
			},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			fmt.Sprintf("%s@%s", fixtures[1].module, fixtures[1].version): {
				ArchiveSHA256: sha256HexForTest(mustArchiveBytesForFixture(t, fixtures[1].fixture)),
				GitSHA:        strings.Repeat("a", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T12:00:00Z",
			},
			fmt.Sprintf("%s@%s", fixtures[0].module, fixtures[0].version): {
				ArchiveSHA256: sha256HexForTest(mustArchiveBytesForFixture(t, fixtures[0].fixture)),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitSource,
				ResolvedAt:    "2026-03-28T12:05:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		beforeTools := toolEntryStrings(file.Tools)

		got, err := file.Prepare(ctx, resolver)
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}

		wantLoaded, err := assembler.Load(ctx, resolver, assembler.Declaration{
			Packages: []assembler.PackageDeclaration{
				{Module: tooldef.ModulePath(fixtures[1].module), Version: tooldef.Version(fixtures[1].version)},
				{Module: tooldef.ModulePath(fixtures[0].module), Version: tooldef.Version(fixtures[0].version)},
			},
		})
		if err != nil {
			t.Fatalf("assembler.Load: %v", err)
		}
		want, err := toolset.PrepareTools(context.Background(), wantLoaded.Tools(), toolset.Config{})
		if err != nil {
			t.Fatalf("PrepareTools: %v", err)
		}

		gotIDs := preparedToolIDs(got)
		wantIDs := preparedToolIDs(want)
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("prepared tools = %#v, want %#v", gotIDs, wantIDs)
		}

		orderedPackages := preparedPackageNames(got)
		wantOrderedPackages := []string{"calc", "calc", "calc", "github-issues"}
		if !reflect.DeepEqual(orderedPackages, wantOrderedPackages) {
			t.Fatalf("prepared package order = %#v, want %#v", orderedPackages, wantOrderedPackages)
		}

		if !reflect.DeepEqual(toolEntryStrings(file.Tools), beforeTools) {
			t.Fatalf("declared tools changed after Prepare: got %#v, want %#v", toolEntryStrings(file.Tools), beforeTools)
		}
	})

	t.Run("CacheLoadFailureSurfacesResolverErrorAndStopsBeforeLaterPackages", func(t *testing.T) {
		cache := newTempCache(t)
		moduleFail := registry.ModulePath("example.com/acme/bad-cache")
		versionFail := registry.Version("v1.0.0")
		if err := cache.Put(moduleFail, versionFail, []byte("not-a-valid-archive"), []byte("{bad")); err != nil {
			t.Fatalf("seed malformed cache: %v", err)
		}

		resolver := registry.NewResolver(cache, &recordingSource{})
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				"example.com/zeta/never-reached": "v9.9.9",
				moduleFail.String():              versionFail.String(),
			},
			"tools": []map[string]string{},
		})

		got, err := file.Prepare(ctx, resolver)
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, fmt.Sprintf("resolve %s@%s", moduleFail, versionFail))
		assertErrorContains(t, err, "cache hit but load failed")
		assertErrorContains(t, err, "parse external manifest")
	})

	t.Run("StopsOnFirstSortedPackageError", func(t *testing.T) {
		sourceErr := errors.New("source boom")
		src := &recordingSource{err: sourceErr}
		resolver := registry.NewResolver(newTempCache(t), src)
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				"example.com/zeta/after-failure": "v2.0.0",
				"example.com/acme/first":         "v1.0.0",
			},
			"tools": []map[string]string{},
		})

		got, err := file.Prepare(ctx, resolver)
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Prepare() tools = %#v, want empty", got.Tools())
		}
		if !errors.Is(err, sourceErr) {
			t.Fatalf("error = %v, want errors.Is(..., %v)", err, sourceErr)
		}
		assertErrorContains(t, err, "resolve example.com/acme/first@v1.0.0")
		assertErrorContains(t, err, "source[0]: source boom")

		wantCalls := []string{"example.com/acme/first@v1.0.0"}
		if !reflect.DeepEqual(src.calls, wantCalls) {
			t.Fatalf("source calls = %#v, want %#v", src.calls, wantCalls)
		}
	})
	t.Run("FirstResolveWritesSiblingLockfileDeterministically", func(t *testing.T) {
		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		metadata := registry.ResolveMetadata{
			ArchiveSHA256: sha256HexForTest(archiveBytes),
			GitSHA:        strings.Repeat("a", 40),
			ResolvedFrom:  registry.ResolvedFromGitHubRelease,
			ResolvedAt:    "2026-03-28T12:00:00Z",
		}
		src := &recordingSource{result: registry.FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: metadata}}
		resolver := registry.NewResolver(newTempCache(t), src)
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"fixtures.local/calc": "v1.2.3",
			},
			"tools": []map[string]string{{"tool": "fixtures.local/calc@v1.2.3/calc.add"}},
		})

		got, err := file.Prepare(ctx, resolver)
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}
		if len(got.Tools()) == 0 {
			t.Fatal("Prepare() returned no tools")
		}

		raw, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		lock, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		entry, ok := lock.Packages["fixtures.local/calc@v1.2.3"]
		if !ok {
			t.Fatalf("lock packages = %#v, want calc entry", lock.Packages)
		}
		if entry.ArchiveSHA256 != metadata.ArchiveSHA256 || entry.GitSHA != metadata.GitSHA || entry.ResolvedAt != metadata.ResolvedAt {
			t.Fatalf("lock entry = %#v, want metadata %#v", entry, metadata)
		}
		if !strings.Contains(string(raw), "fixtures.local/calc@v1.2.3") {
			t.Fatalf("lockfile bytes = %q, want package key", string(raw))
		}
	})

	t.Run("ExistingLockSchemaValidationFailureAbortsBeforeResolution", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		if err := os.WriteFile(file.LockFilename(), []byte(`{"packages":{"example.com/acme/calc@v1.2.3":{"archive_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","git_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","resolved_from":"github-release"}}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{result: registry.FetchResult{Archive: []byte("unused"), Manifest: []byte("unused")}}

		_, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want lock validation error")
		}
		assertErrorContains(t, err, "schema-validate toolset lock file")
		assertErrorContains(t, err, file.LockFilename())
		if len(src.calls) != 0 {
			t.Fatalf("source calls = %#v, want none", src.calls)
		}
	})

	t.Run("ExistingLockSemanticValidationFailureAbortsBeforeResolution", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		if err := os.WriteFile(file.LockFilename(), []byte(`{"packages":{"bad-key":{"archive_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","git_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","resolved_from":"github-release","resolved_at":"2026-03-28T12:00:00Z"}}}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{result: registry.FetchResult{Archive: []byte("unused"), Manifest: []byte("unused")}}

		_, err := file.Prepare(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want lock validation error")
		}
		assertErrorContains(t, err, "validate toolset lock file")
		assertErrorContains(t, err, `packages["bad-key"]`)
		if len(src.calls) != 0 {
			t.Fatalf("source calls = %#v, want none", src.calls)
		}
	})

	t.Run("MatchingExistingLockUsesExpectedMetadataAndAcceptsCacheHit", func(t *testing.T) {
		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath("fixtures.local/calc"), registry.Version("v1.2.3"), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"fixtures.local/calc": "v1.2.3"},
			"tools":    []map[string]string{{"tool": "fixtures.local/calc@v1.2.3/calc.add"}},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"fixtures.local/calc@v1.2.3": {
				ArchiveSHA256: sha256HexForTest(archiveBytes),
				GitSHA:        strings.Repeat("c", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{err: fmt.Errorf("should not fetch on matching cache hit")}

		_, err := file.Prepare(ctx, registry.NewResolver(cache, src))
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}
		if len(src.calls) != 0 {
			t.Fatalf("source calls = %#v, want none", src.calls)
		}
		reloaded, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		if !reflect.DeepEqual(reloaded.Packages, locked.Packages) {
			t.Fatalf("rewritten lock packages = %#v, want %#v", reloaded.Packages, locked.Packages)
		}
	})

	t.Run("CacheMismatchRefetchSuccessRewritesLockWithFreshMetadata", func(t *testing.T) {
		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath("fixtures.local/calc"), registry.Version("v1.2.3"), []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"fixtures.local/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"fixtures.local/calc@v1.2.3": {
				ArchiveSHA256: strings.Repeat("d", 64),
				GitSHA:        strings.Repeat("e", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		freshMetadata := registry.ResolveMetadata{
			ArchiveSHA256: locked.Packages["fixtures.local/calc@v1.2.3"].ArchiveSHA256,
			GitSHA:        strings.Repeat("f", 40),
			ResolvedFrom:  registry.ResolvedFromGitSource,
			ResolvedAt:    "2026-03-28T14:00:00Z",
		}
		freshMetadata.ArchiveSHA256 = sha256HexForTest(archiveBytes)
		locked.Packages["fixtures.local/calc@v1.2.3"] = ToolsetLockEntry{
			ArchiveSHA256: freshMetadata.ArchiveSHA256,
			GitSHA:        strings.Repeat("e", 40),
			ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
			ResolvedAt:    "2026-03-28T13:00:00Z",
		}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{result: registry.FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: freshMetadata}}

		_, err := file.Prepare(ctx, registry.NewResolver(cache, src))
		if err != nil {
			t.Fatalf("Prepare() error: %v", err)
		}
		if len(src.calls) != 1 {
			t.Fatalf("source calls = %#v, want one refetch", src.calls)
		}
		reloaded, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		entry := reloaded.Packages["fixtures.local/calc@v1.2.3"]
		if entry.ArchiveSHA256 != freshMetadata.ArchiveSHA256 || entry.GitSHA != freshMetadata.GitSHA || entry.ResolvedFrom != ToolsetLockResolvedFrom(freshMetadata.ResolvedFrom) || entry.ResolvedAt != freshMetadata.ResolvedAt {
			t.Fatalf("lock entry = %#v, want fresh metadata %#v", entry, freshMetadata)
		}
	})

	t.Run("PersistentLockMismatchFailsAndLeavesLockfileUntouched", func(t *testing.T) {
		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath("fixtures.local/calc"), registry.Version("v1.2.3"), []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"fixtures.local/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"fixtures.local/calc@v1.2.3": {
				ArchiveSHA256: sha256HexForTest(archiveBytes),
				GitSHA:        strings.Repeat("a", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		badMetadata := registry.ResolveMetadata{
			ArchiveSHA256: strings.Repeat("b", 64),
			GitSHA:        strings.Repeat("c", 40),
			ResolvedFrom:  registry.ResolvedFromGitSource,
			ResolvedAt:    "2026-03-28T14:00:00Z",
		}
		src := &recordingSource{result: registry.FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: badMetadata}}

		_, err = file.Prepare(ctx, registry.NewResolver(cache, src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want mismatch error")
		}
		assertErrorContains(t, err, "cache mismatch refetch failed integrity check")
		assertErrorContains(t, err, "expected archive_sha256")
		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed on mismatch failure:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})

	t.Run("FirstSortedPackageFailureLeavesPriorLockfileUntouched", func(t *testing.T) {
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{
				"example.com/zeta/after-failure": "v2.0.0",
				"example.com/acme/first":         "v1.0.0",
			},
			"tools": []map[string]string{},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/first@v1.0.0": {
				ArchiveSHA256: strings.Repeat("a", 64),
				GitSHA:        strings.Repeat("b", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		before, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		sourceErr := errors.New("source boom")
		src := &recordingSource{err: sourceErr}

		_, err = file.Prepare(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Prepare() error = nil, want non-nil")
		}
		if !errors.Is(err, sourceErr) {
			t.Fatalf("error = %v, want source boom", err)
		}
		wantCalls := []string{"example.com/acme/first@v1.0.0"}
		if !reflect.DeepEqual(src.calls, wantCalls) {
			t.Fatalf("source calls = %#v, want %#v", src.calls, wantCalls)
		}
		after, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		if string(after) != string(before) {
			t.Fatalf("lockfile changed on prepare failure:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
		}
	})
}

type recordingSource struct {
	calls  []string
	result registry.FetchResult
	err    error
}

func (s *recordingSource) Fetch(_ context.Context, module registry.ModulePath, version registry.Version) (registry.FetchResult, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s@%s", module, version))
	return s.result, s.err
}

func TestToolsetFilePutPackageVersion(t *testing.T) {
	t.Parallel()

	t.Run("AddsNewPackageWithoutChangingExistingTools", func(t *testing.T) {
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
			},
		})

		if err := file.PutPackageVersion("example.com/acme/echo", "v2.0.0"); err != nil {
			t.Fatalf("PutPackageVersion(): %v", err)
		}
		if got := file.Packages["example.com/acme/echo"]; got != "v2.0.0" {
			t.Fatalf("packages[echo] = %q, want v2.0.0", got)
		}
		if got := file.Tools[0].Tool; got != "example.com/acme/calc@v1.2.3/calc.add" {
			t.Fatalf("tools[0] = %q, want existing tool unchanged", got)
		}
	})

	t.Run("UpdatesExistingPackageAndRewritesToolVersions", func(t *testing.T) {
		file := mustLoadToolsetFile(t, map[string]any{
			"packages": map[string]string{
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{
				{"tool": "example.com/acme/calc@v1.2.3/calc.add"},
				{"tool": "example.com/acme/calc@v1.2.3/calc.sub"},
			},
		})

		if err := file.PutPackageVersion("example.com/acme/calc", "v1.3.0"); err != nil {
			t.Fatalf("PutPackageVersion(): %v", err)
		}
		if got := file.Packages["example.com/acme/calc"]; got != "v1.3.0" {
			t.Fatalf("packages[calc] = %q, want v1.3.0", got)
		}
		gotTools := []string{file.Tools[0].Tool, file.Tools[1].Tool}
		wantTools := []string{
			"example.com/acme/calc@v1.3.0/calc.add",
			"example.com/acme/calc@v1.3.0/calc.sub",
		}
		if !reflect.DeepEqual(gotTools, wantTools) {
			t.Fatalf("tools = %#v, want %#v", gotTools, wantTools)
		}
	})
}

func mustLoadToolsetFileNamed(t *testing.T, basename string, value any) *ToolsetFile {
	t.Helper()

	file, err := Load(writeToolsetJSONNamed(t, basename, value))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return file
}

func sha256HexForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustLoadToolsetFile(t *testing.T, value any) *ToolsetFile {
	t.Helper()

	file, err := Load(writeToolsetJSON(t, value))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return file
}

func newTempCache(t *testing.T) *registry.Cache {
	t.Helper()
	cache, err := registry.NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache() error: %v", err)
	}
	return cache
}

func toolEntryStrings(entries []ToolEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Tool
	}
	return out
}

func preparedToolIDs(prepared toolset.PreparedToolset) []string {
	tools := prepared.Tools()
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.PackageMeta.Name + "/" + tool.Name
	}
	return out
}

func preparedPackageNames(prepared toolset.PreparedToolset) []string {
	tools := prepared.Tools()
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.PackageMeta.Name
	}
	return out
}

func mustArchiveBytesForFixture(t *testing.T, fixtureName string) []byte {
	t.Helper()
	archiveBytes, _ := loadFixtureArchiveAndManifestBytes(t, fixtureName)
	return archiveBytes
}

func loadFixtureArchiveAndManifestBytes(t *testing.T, fixtureName string) ([]byte, []byte) {
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

	matches, err := filepath.Glob(filepath.Join(fixtureDir, "*.toolbox.pkg"))
	if err != nil {
		t.Fatalf("Glob(%q): %v", filepath.Join(fixtureDir, "*.toolbox.pkg"), err)
	}
	if len(matches) != 1 {
		t.Fatalf("fixture %q archive matches = %#v, want exactly one", fixtureName, matches)
	}

	archiveBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read archive fixture %s: %v", matches[0], err)
	}
	manifestPath := filepath.Join(fixtureDir, "toolbox.pkg.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest fixture %s: %v", manifestPath, err)
	}
	return archiveBytes, manifestBytes
}

func loadSourceFixtureDir(t *testing.T, fixtureName string) string {
	t.Helper()
	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == fixtureName {
			return dir
		}
	}
	t.Fatalf("source fixture %q not found", fixtureName)
	return ""
}

func copyFixtureDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q): %v", dstPath, err)
			}
			copyFixtureDir(t, srcPath, dstPath)
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", dstPath, err)
		}
	}
}

func rewriteSourceFixtureModule(t *testing.T, dir, module string) {
	t.Helper()
	manifestPath := filepath.Join(dir, packaging.DevManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", manifestPath, err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", manifestPath, err)
	}
	manifest["module"] = module
	writeToolsetLocalJSON(t, manifestPath, manifest)
}

func writeToolsetJSON(t *testing.T, value any) string {
	t.Helper()
	return writeToolsetJSONNamed(t, "toolbox.toolset.json", value)
}

func writeToolsetLocalJSON(t *testing.T, filename string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filename, err)
	}
}

func writeToolsetJSONNamed(t *testing.T, basename string, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	filename := filepath.Join(t.TempDir(), basename)
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filename, err)
	}
	return filename
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err.Error(), want)
	}
}
