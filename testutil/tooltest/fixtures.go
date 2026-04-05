package tooltest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

// LocalSrcToolDir returns the absolute path to a named source fixture package.
func LocalSrcToolDir(name string) string {
	return filepath.Join(tooltestDir(), "..", "fixtures", "toolbox.pkgs", name)
}

// PackageDistToolDir returns the absolute path to a named dist fixture package directory.
// The input name should be the base tool/package name without the "-dist" suffix.
func PackageDistToolDir(name string) string {
	return filepath.Join(tooltestDir(), "..", "testdata", "goldens", "distpkgs", name+"-dist")
}

// SrcDirs returns the absolute paths of all valid source package fixture
// directories (those containing a toolbox.devpkg.json).
func SrcDirs() []string {
	pkgsDir := filepath.Join(tooltestDir(), "..", "fixtures", "toolbox.pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if err != nil {
		panic("tooltest: read toolbox.pkgs dir: " + err.Error())
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(pkgsDir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, packaging.DevManifestFilename)); err == nil {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// DistDirs returns the absolute paths of all dist package golden directories
// (those ending in -dist and containing a toolbox.pkg.json).
func DistDirs() []string {
	distpkgsDir := filepath.Join(tooltestDir(), "..", "testdata", "goldens", "distpkgs")
	entries, err := os.ReadDir(distpkgsDir)
	if err != nil {
		panic("tooltest: read testdata/goldens/distpkgs dir: " + err.Error())
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-dist") {
			continue
		}
		dir := filepath.Join(distpkgsDir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, packaging.PkgManifestFilename)); err == nil {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// LocalPackageDecl returns a declaration that loads one or more local source
// packages. Each input may be either a fixture name (for example "calc") or an
// explicit source package directory.
func LocalPackageDecl(nameOrDir ...string) assembler.Declaration {
	decl := assembler.Declaration{Packages: make([]assembler.PackageDeclaration, 0, len(nameOrDir))}
	for _, item := range nameOrDir {
		dir, name := resolveLocalPackageInput(item)
		module := fixtureModule(name)
		if isExplicitLocalPackageInput(item) {
			loaded, err := packaging.LoadDev(dir)
			if err != nil {
				panic("tooltest: load local package " + dir + ": " + err.Error())
			}
			module = loaded.Package.Module
		}
		decl.Packages = append(decl.Packages, assembler.PackageDeclaration{
			Module:     module,
			Version:    fixtureVersion(),
			ReplaceDir: dir,
		})
	}
	return decl
}

// DistPackageDecl returns a declaration that loads one or more packaged dist
// fixtures. Each input may be either a fixture name (for example "calc") or an
// explicit dist package directory.
func DistPackageDecl(nameOrDir ...string) assembler.Declaration {
	decl := assembler.Declaration{Packages: make([]assembler.PackageDeclaration, 0, len(nameOrDir))}
	for _, item := range nameOrDir {
		distDir, name := resolveDistPackageInput(item)
		decl.Packages = append(decl.Packages, assembler.PackageDeclaration{
			Module:       fixtureModule(name),
			Version:      fixtureVersion(),
			ArchivePath:  filepath.Join(distDir, name+".toolbox.pkg"),
			ManifestPath: filepath.Join(distDir, packaging.PkgManifestFilename),
		})
	}
	return decl
}

func resolveLocalPackageInput(nameOrDir string) (dir, name string) {
	if isExplicitLocalPackageInput(nameOrDir) {
		clean := filepath.Clean(nameOrDir)
		return clean, filepath.Base(clean)
	}
	return LocalSrcToolDir(nameOrDir), nameOrDir
}

func isExplicitLocalPackageInput(nameOrDir string) bool {
	return filepath.IsAbs(nameOrDir) || strings.ContainsRune(nameOrDir, filepath.Separator)
}

func resolveDistPackageInput(nameOrDir string) (dir, name string) {
	if filepath.IsAbs(nameOrDir) || strings.ContainsRune(nameOrDir, filepath.Separator) {
		clean := filepath.Clean(nameOrDir)
		base := filepath.Base(clean)
		return clean, strings.TrimSuffix(base, "-dist")
	}
	return PackageDistToolDir(nameOrDir), nameOrDir
}

func fixtureModule(name string) tooldef.ModulePath {
	return tooldef.ModulePath("fixtures.local/" + name)
}

func fixtureVersion() tooldef.Version {
	return tooldef.Version("v0.0.0")
}

// PrepareToolset loads tools from a declaration and prepares them with the
// given toolset config. It is intended for tests using local fixture packages
// and packaged fixture archives.
func PrepareToolset(t testing.TB, decl assembler.Declaration, cfg toolset.Config) toolset.PreparedToolset {
	t.Helper()

	loadedPkgs, err := assembler.Load(context.Background(), nil, decl)
	if err != nil {
		t.Fatalf("load tool declaration: %v", err)
	}
	prepared, err := toolset.PrepareTools(context.Background(), loadedPkgs.Tools(), cfg)
	if err != nil {
		t.Fatalf("prepare toolset: %v", err)
	}
	return prepared
}

func tooltestDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Dir(file)
}
