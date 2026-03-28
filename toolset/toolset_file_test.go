package toolset

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

	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
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

func TestToolsetFileResolve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("NilToolsetRejected", func(t *testing.T) {
		var file *ToolsetFile

		got, err := file.Resolve(ctx, registry.NewResolver(newTempCache(t)))
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Resolve() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, "nil toolset file")
	})

	t.Run("UnvalidatedToolsetRejected", func(t *testing.T) {
		file := &ToolsetFile{Packages: map[string]string{}, Tools: []ToolEntry{}}

		got, err := file.Resolve(ctx, registry.NewResolver(newTempCache(t)))
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Resolve() tools = %#v, want empty", got.Tools())
		}
		assertErrorContains(t, err, "must be loaded and validated before resolve")
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

		got, err := file.Resolve(ctx, nil)
		if err == nil {
			t.Fatal("Resolve() error = nil, want ErrNoResolver")
		}
		if !errors.Is(err, ErrNoResolver) {
			t.Fatalf("error = %v, want ErrNoResolver", err)
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Resolve() tools = %#v, want empty", got.Tools())
		}
		if !reflect.DeepEqual(toolEntryStrings(file.Tools), beforeTools) {
			t.Fatalf("declared tools changed after Resolve error: got %#v, want %#v", toolEntryStrings(file.Tools), beforeTools)
		}
	})

	t.Run("ResolvesFromPrePopulatedCacheMatchesImperativeBuilderInSortedPackageOrder", func(t *testing.T) {
		fixtures := []struct {
			module  string
			version string
			fixture string
		}{
			{module: "example.com/zeta/github-issues", version: "v2.0.0", fixture: "github-issues-dist"},
			{module: "example.com/acme/calc", version: "v1.2.3", fixture: "calc-dist"},
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

		got, err := file.Resolve(ctx, resolver)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}

		wantBuilder := NewWithResolver(resolver)
		if err := wantBuilder.AddFromRegistry(ctx, fixtures[1].module, fixtures[1].version); err != nil {
			t.Fatalf("imperative AddFromRegistry(%s): %v", fixtures[1].module, err)
		}
		if err := wantBuilder.AddFromRegistry(ctx, fixtures[0].module, fixtures[0].version); err != nil {
			t.Fatalf("imperative AddFromRegistry(%s): %v", fixtures[0].module, err)
		}
		want := wantBuilder.Resolve()

		gotIDs := resolvedToolIDs(got)
		wantIDs := resolvedToolIDs(want)
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("resolved tools = %#v, want %#v", gotIDs, wantIDs)
		}

		orderedPackages := resolvedPackageNames(got)
		wantOrderedPackages := []string{"calc", "calc", "calc", "github-issues"}
		if !reflect.DeepEqual(orderedPackages, wantOrderedPackages) {
			t.Fatalf("resolved package order = %#v, want %#v", orderedPackages, wantOrderedPackages)
		}

		if !reflect.DeepEqual(toolEntryStrings(file.Tools), beforeTools) {
			t.Fatalf("declared tools changed after Resolve: got %#v, want %#v", toolEntryStrings(file.Tools), beforeTools)
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

		got, err := file.Resolve(ctx, resolver)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Resolve() tools = %#v, want empty", got.Tools())
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

		got, err := file.Resolve(ctx, resolver)
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
		}
		if len(got.Tools()) != 0 {
			t.Fatalf("Resolve() tools = %#v, want empty", got.Tools())
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
				"example.com/acme/calc": "v1.2.3",
			},
			"tools": []map[string]string{{"tool": "example.com/acme/calc@v1.2.3/calc.add"}},
		})

		got, err := file.Resolve(ctx, resolver)
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if len(got.Tools()) == 0 {
			t.Fatal("Resolve() returned no tools")
		}

		raw, err := os.ReadFile(file.LockFilename())
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file.LockFilename(), err)
		}
		lock, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		entry, ok := lock.Packages["example.com/acme/calc@v1.2.3"]
		if !ok {
			t.Fatalf("lock packages = %#v, want calc entry", lock.Packages)
		}
		if entry.ArchiveSHA256 != metadata.ArchiveSHA256 || entry.GitSHA != metadata.GitSHA || entry.ResolvedAt != metadata.ResolvedAt {
			t.Fatalf("lock entry = %#v, want metadata %#v", entry, metadata)
		}
		if !strings.Contains(string(raw), "example.com/acme/calc@v1.2.3") {
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

		_, err := file.Resolve(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Resolve() error = nil, want lock validation error")
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

		_, err := file.Resolve(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Resolve() error = nil, want lock validation error")
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
		if err := cache.Put(registry.ModulePath("example.com/acme/calc"), registry.Version("v1.2.3"), archiveBytes, manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{{"tool": "example.com/acme/calc@v1.2.3/calc.add"}},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/calc@v1.2.3": {
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

		_, err := file.Resolve(ctx, registry.NewResolver(cache, src))
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
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
		if err := cache.Put(registry.ModulePath("example.com/acme/calc"), registry.Version("v1.2.3"), []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/calc@v1.2.3": {
				ArchiveSHA256: strings.Repeat("d", 64),
				GitSHA:        strings.Repeat("e", 40),
				ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
				ResolvedAt:    "2026-03-28T13:00:00Z",
			},
		}}
		freshMetadata := registry.ResolveMetadata{
			ArchiveSHA256: locked.Packages["example.com/acme/calc@v1.2.3"].ArchiveSHA256,
			GitSHA:        strings.Repeat("f", 40),
			ResolvedFrom:  registry.ResolvedFromGitSource,
			ResolvedAt:    "2026-03-28T14:00:00Z",
		}
		freshMetadata.ArchiveSHA256 = sha256HexForTest(archiveBytes)
		locked.Packages["example.com/acme/calc@v1.2.3"] = ToolsetLockEntry{
			ArchiveSHA256: freshMetadata.ArchiveSHA256,
			GitSHA:        strings.Repeat("e", 40),
			ResolvedFrom:  ToolsetLockResolvedFromGitHubRelease,
			ResolvedAt:    "2026-03-28T13:00:00Z",
		}
		if err := locked.Write(file.LockFilename()); err != nil {
			t.Fatalf("Write(%q): %v", file.LockFilename(), err)
		}
		src := &recordingSource{result: registry.FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: freshMetadata}}

		_, err := file.Resolve(ctx, registry.NewResolver(cache, src))
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if len(src.calls) != 1 {
			t.Fatalf("source calls = %#v, want one refetch", src.calls)
		}
		reloaded, err := LoadLock(file.LockFilename())
		if err != nil {
			t.Fatalf("LoadLock(%q): %v", file.LockFilename(), err)
		}
		entry := reloaded.Packages["example.com/acme/calc@v1.2.3"]
		if entry.ArchiveSHA256 != freshMetadata.ArchiveSHA256 || entry.GitSHA != freshMetadata.GitSHA || entry.ResolvedFrom != ToolsetLockResolvedFrom(freshMetadata.ResolvedFrom) || entry.ResolvedAt != freshMetadata.ResolvedAt {
			t.Fatalf("lock entry = %#v, want fresh metadata %#v", entry, freshMetadata)
		}
	})

	t.Run("PersistentLockMismatchFailsAndLeavesLockfileUntouched", func(t *testing.T) {
		archiveBytes, manifestBytes := loadFixtureArchiveAndManifestBytes(t, "calc-dist")
		cache := newTempCache(t)
		if err := cache.Put(registry.ModulePath("example.com/acme/calc"), registry.Version("v1.2.3"), []byte("stale archive bytes"), manifestBytes); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
		file := mustLoadToolsetFileNamed(t, "support-agent.toolset.json", map[string]any{
			"packages": map[string]string{"example.com/acme/calc": "v1.2.3"},
			"tools":    []map[string]string{},
		})
		locked := &ToolsetLockFile{Packages: map[string]ToolsetLockEntry{
			"example.com/acme/calc@v1.2.3": {
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

		_, err = file.Resolve(ctx, registry.NewResolver(cache, src))
		if err == nil {
			t.Fatal("Resolve() error = nil, want mismatch error")
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

		_, err = file.Resolve(ctx, registry.NewResolver(newTempCache(t), src))
		if err == nil {
			t.Fatal("Resolve() error = nil, want non-nil")
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
			t.Fatalf("lockfile changed on resolve failure:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
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

func toolEntryStrings(entries []ToolEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Tool
	}
	return out
}

func resolvedToolIDs(resolved ResolvedToolset) []string {
	tools := resolved.Tools()
	out := make([]string, len(tools))
	for i, resolvedTool := range tools {
		out[i] = resolvedTool.Package.Name + "/" + resolvedTool.Name
	}
	return out
}

func resolvedPackageNames(resolved ResolvedToolset) []string {
	tools := resolved.Tools()
	out := make([]string, len(tools))
	for i, resolvedTool := range tools {
		out[i] = resolvedTool.Package.Name
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

func writeToolsetJSON(t *testing.T, value any) string {
	t.Helper()
	return writeToolsetJSONNamed(t, "toolbox.toolset.json", value)
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
