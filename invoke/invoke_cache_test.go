package invoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestCacheKeyStableAcrossEquivalentPreparedPackages(t *testing.T) {
	resetCheckSessionsForTest()

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

	if got, err := RunContext(context.Background(), first, "calc.add", map[string]any{"a": 2, "b": 3}); err != nil {
		t.Fatalf("RunContext(first): %v", err)
	} else if got != "5" {
		t.Fatalf("RunContext(first) = %q, want %q", got, "5")
	}
	if got := checkSessionCountForTest(); got != 1 {
		t.Fatalf("checkSessions after first run = %d, want 1", got)
	}

	if got, err := RunContext(context.Background(), second, "calc.add", map[string]any{"a": 4, "b": 5}); err != nil {
		t.Fatalf("RunContext(second): %v", err)
	} else if got != "9" {
		t.Fatalf("RunContext(second) = %q, want %q", got, "9")
	}
	if got := checkSessionCountForTest(); got != 1 {
		t.Fatalf("checkSessions after second run = %d, want 1", got)
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

func resetCheckSessionsForTest() {
	checkSessionsMu.Lock()
	defer checkSessionsMu.Unlock()
	checkSessions = map[string]*toolbox.CheckSession{}
}

func checkSessionCountForTest() int {
	checkSessionsMu.RLock()
	defer checkSessionsMu.RUnlock()
	return len(checkSessions)
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
