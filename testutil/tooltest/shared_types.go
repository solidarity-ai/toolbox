package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/toolset"
)

func sharedTypesFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "shared-types")
}

// SharedTypesBuilder returns a *toolset.Builder loaded with the shared-types fixture package.
// The caller can then call builder.Resolve(cfg) with any desired Config.
func SharedTypesBuilder(t testing.TB) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(sharedTypesFixtureDir()); err != nil {
		t.Fatalf("add shared-types package dir: %v", err)
	}
	return builder
}
