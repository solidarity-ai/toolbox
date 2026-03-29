package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

// FetchTestToolset loads the fetch-test fixture into a resolved toolset.
func FetchTestToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(fetchTestFixtureDir()); err != nil {
		t.Fatalf("add fetch-test package dir: %v", err)
	}
	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve fetch-test toolset: %v", err)
	}
	return resolved
}

func fetchTestFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "fetch-test")
}
