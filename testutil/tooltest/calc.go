package tooltest

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func CalcAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.add")
}

func CalcSub(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.sub")
}

func CalcAsyncAdd(t testing.TB) tooldef.TSToolDef {
	t.Helper()

	return mustCalcTool(t, "calc.asyncAdd")
}

func CalcToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(calcFixtureDir()); err != nil {
		t.Fatalf("add calc package dir: %v", err)
	}
	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}
	return resolved
}

// CalcDistToolset loads the calc-dist golden fixture (archive) into a resolved toolset.
func CalcDistToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	distDir := calcDistFixtureDir()
	builder := toolset.New()
	archivePath := filepath.Join(distDir, "calc.toolbox.pkg")
	manifestPath := filepath.Join(distDir, packaging.PkgManifestFilename)
	if err := builder.AddFromArchive(archivePath, manifestPath); err != nil {
		t.Fatalf("add calc-dist archive: %v", err)
	}
	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}
	return resolved
}

func calcDistFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "calc-dist")
}

// VFSTestDistToolset loads the vfs-test-dist golden fixture (archive) into a resolved toolset.
func VFSTestDistToolset(t testing.TB) toolset.ResolvedToolset {
	t.Helper()

	distDir := vfsTestDistFixtureDir()
	builder := toolset.New()
	archivePath := filepath.Join(distDir, "vfs-test.toolbox.pkg")
	manifestPath := filepath.Join(distDir, packaging.PkgManifestFilename)
	if err := builder.AddFromArchive(archivePath, manifestPath); err != nil {
		t.Fatalf("add vfs-test-dist archive: %v", err)
	}
	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}
	return resolved
}

func vfsTestDistFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "vfs-test-dist")
}

func mustCalcTool(t testing.TB, name string) tooldef.TSToolDef {
	t.Helper()

	pkg, err := packaging.LoadDev(calcFixtureDir())
	if err != nil {
		t.Fatalf("load calc package: %v", err)
	}
	for _, tool := range pkg.ResolvedTools() {
		if tool.Name == name {
			if tool.TS == nil {
				t.Fatalf("tool %s has no TS definition", name)
			}
			return *tool.TS
		}
	}
	t.Fatalf("expected tool %s", name)
	return tooldef.TSToolDef{}
}

func calcFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "calc")
}

// CalcBuilder returns a *toolset.Builder loaded with the calc fixture package.
// The caller can then call builder.Resolve(cfg) with any desired Config.
func CalcBuilder(t testing.TB) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(calcFixtureDir()); err != nil {
		t.Fatalf("add calc package dir: %v", err)
	}
	return builder
}
