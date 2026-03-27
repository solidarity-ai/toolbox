package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func edgeCasesFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "edge-cases")
}

// EdgeCasesBuilder returns a *toolset.Builder loaded with the edge-cases fixture package.
// The caller can then call builder.Resolve(cfg) with any desired Config.
func EdgeCasesBuilder(t testing.TB) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(edgeCasesFixtureDir()); err != nil {
		t.Fatalf("add edge-cases package dir: %v", err)
	}
	return builder
}
