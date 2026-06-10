package invoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/vfs"
)

func TestCacheKeyStableAcrossEquivalentPreparedPackages(t *testing.T) {
	exec := NewExecutor()
	t.Cleanup(func() { _ = exec.Close() })

	first := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
	second := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})

	firstTool, err := findTool(first, "calc.add")
	if err != nil {
		t.Fatalf("findTool(first): %v", err)
	}
	secondTool, err := findTool(second, "calc.add")
	if err != nil {
		t.Fatalf("findTool(second): %v", err)
	}

	if got, want := firstTool.CacheKey(), secondTool.CacheKey(); got != want {
		t.Fatalf("CacheKey mismatch:\nfirst:  %q\nsecond: %q", got, want)
	}

	if gotWire, err := exec.RunContext(context.Background(), first, "calc.add", tooltest.WireArgs(t, map[string]any{"a": 2, "b": 3})); err != nil {
		t.Fatalf("RunContext(first): %v", err)
	} else if got := tooltest.WireString(t, gotWire); got != "5" {
		t.Fatalf("RunContext(first) = %q, want %q", got, "5")
	}
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("checkSessions after first run = %d, want 1", got)
	}

	if gotWire, err := exec.RunContext(context.Background(), second, "calc.add", tooltest.WireArgs(t, map[string]any{"a": 4, "b": 5})); err != nil {
		t.Fatalf("RunContext(second): %v", err)
	} else if got := tooltest.WireString(t, gotWire); got != "9" {
		t.Fatalf("RunContext(second) = %q, want %q", got, "9")
	}
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("checkSessions after second run = %d, want 1", got)
	}
}

func TestCancelableContextPopulatesCheckSessionCache(t *testing.T) {
	exec := NewExecutor()
	t.Cleanup(func() { _ = exec.Close() })

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotWire, err := exec.RunContext(ctx, prepared, "calc.add", tooltest.WireArgs(t, map[string]any{"a": 2, "b": 3}))
	if err != nil {
		t.Fatalf("RunContext(cancelable): %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != "5" {
		t.Fatalf("RunContext(cancelable) = %q, want %q", got, "5")
	}
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("checkSessions after cancelable run = %d, want 1", got)
	}
}

func TestTimedOutRuntimeKeepsCheckSessionCache(t *testing.T) {
	exec := NewExecutor()
	t.Cleanup(func() { _ = exec.Close() })

	workDir := filepath.Join(t.TempDir(), "calc")
	copyDir(t, tooltest.LocalSrcToolDir("calc"), workDir)

	waitPath := filepath.Join(workDir, "tools", "calc.wait.ts")
	if err := os.WriteFile(waitPath, []byte(`export default async function tool(): Promise<string> {
  await new Promise(() => {});
  return "done";
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", waitPath, err)
	}

	manifestPath := filepath.Join(workDir, "toolbox.devpkg.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", manifestPath, err)
	}
	updatedManifest := strings.Replace(
		string(manifestData),
		`    { "entry_ts": "tools/calc.async-add.ts" }`,
		`    { "entry_ts": "tools/calc.async-add.ts" },
    { "entry_ts": "tools/calc.wait.ts" }`,
		1,
	)
	if updatedManifest == string(manifestData) {
		t.Fatalf("manifest %q did not contain expected calc.async-add entry", manifestPath)
	}
	if err := os.WriteFile(manifestPath, []byte(updatedManifest), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", manifestPath, err)
	}

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(workDir), toolset.Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := exec.RunContext(ctx, prepared, "calc.wait", tooltest.WireArgs(t, map[string]any{})); err == nil {
		t.Fatal("RunContext(timeout) error = nil, want timeout")
	}
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("checkSessions after timed out run = %d, want 1", got)
	}

	gotWire, err := exec.RunContext(context.Background(), prepared, "calc.add", tooltest.WireArgs(t, map[string]any{"a": 2, "b": 3}))
	if err != nil {
		t.Fatalf("RunContext(reuse after timeout): %v", err)
	}
	got := tooltest.WireString(t, gotWire)
	if got != "5" {
		t.Fatalf("RunContext(reuse after timeout) = %q, want %q", got, "5")
	}
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("checkSessions after reuse run = %d, want 1", got)
	}
}

func TestNewExecutorWithPreparedPopulatesCache(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
	exec := NewExecutor(prepared)
	t.Cleanup(func() { _ = exec.Close() })

	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("active check sessions after constructor = %d, want 1", got)
	}

	exec.SetPrepared(prepared)
	if got := checkSessionCountForTest(exec); got != 1 {
		t.Fatalf("active check sessions after SetPrepared = %d, want 1", got)
	}
}

func TestStartVFSServerUsesUniqueSocketPaths(t *testing.T) {
	firstPath, firstCleanup, err := startVFSServer(vfs.NewMemFS())
	if err != nil {
		t.Fatalf("startVFSServer(first): %v", err)
	}
	defer firstCleanup()

	secondPath, secondCleanup, err := startVFSServer(vfs.NewMemFS())
	if err != nil {
		t.Fatalf("startVFSServer(second): %v", err)
	}
	defer secondCleanup()

	if firstPath == secondPath {
		t.Fatalf("startVFSServer paths must be unique, got %q", firstPath)
	}
}

func TestCacheKeyDiffersForDifferentPackages(t *testing.T) {
	calc := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})
	edge := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("edge-cases"), toolset.Config{})

	calcTool, err := findTool(calc, "calc.add")
	if err != nil {
		t.Fatalf("findTool(calc): %v", err)
	}
	edgeTool, err := findTool(edge, "edgeCases.minimal")
	if err != nil {
		t.Fatalf("findTool(edge): %v", err)
	}

	if got, want := calcTool.CacheKey() == edgeTool.CacheKey(), false; got != want {
		t.Fatalf("CacheKey(calc) == CacheKey(edge) = %v, want %v", got, want)
	}
}

func TestCacheKeyChangesWhenLocalPackageSourceChanges(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "calc")
	copyDir(t, tooltest.LocalSrcToolDir("calc"), workDir)

	first := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(workDir), toolset.Config{})
	firstTool, err := findTool(first, "calc.add")
	if err != nil {
		t.Fatalf("findTool(first): %v", err)
	}
	firstKey := firstTool.CacheKey()
	if firstKey == "" {
		t.Fatal("first CacheKey is empty")
	}

	toolPath := filepath.Join(workDir, "tools", "calc.add.ts")
	data, err := os.ReadFile(toolPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", toolPath, err)
	}
	updated := string(data)
	updated = strings.Replace(updated, "return a + b;", "return a + b + 1;", 1)
	if updated == string(data) {
		t.Fatalf("test fixture %q did not contain expected source", toolPath)
	}
	if err := os.WriteFile(toolPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", toolPath, err)
	}

	second := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(workDir), toolset.Config{})
	secondTool, err := findTool(second, "calc.add")
	if err != nil {
		t.Fatalf("findTool(second): %v", err)
	}
	secondKey := secondTool.CacheKey()
	if secondKey == "" {
		t.Fatal("second CacheKey is empty")
	}
	if firstKey == secondKey {
		t.Fatalf("CacheKey did not change after source edit:\nfirst:  %q\nsecond: %q", firstKey, secondKey)
	}
}

func checkSessionCountForTest(exec *Executor) int {
	if exec == nil {
		return 0
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	count := 0
	for _, entry := range exec.checkSessions {
		entry.mu.Lock()
		if entry.session != nil {
			count++
		}
		entry.mu.Unlock()
	}
	return count
}

func copyDir(t *testing.T, srcDir, dstDir string) {
	t.Helper()

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", srcDir, dstDir, err)
	}
}
