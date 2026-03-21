package fixtures

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/solidarity-ai/toolbox/packaging"
)

// SourceDirs returns the absolute paths of all valid source package fixture
// directories (those containing a toolbox.devpkg.json).
func SourceDirs() []string {
	pkgsDir := filepath.Join(fixturesDir(), "toolbox.pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if err != nil {
		panic("fixtures: read toolbox.pkgs dir: " + err.Error())
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
	return dirs
}

// DistDirs returns the absolute paths of all dist package fixture directories
// (those ending in -dist and containing a toolbox.pkg.json).
func DistDirs() []string {
	pkgsDir := filepath.Join(fixturesDir(), "toolbox.pkgs")
	entries, err := os.ReadDir(pkgsDir)
	if err != nil {
		panic("fixtures: read toolbox.pkgs dir: " + err.Error())
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-dist") {
			continue
		}
		dir := filepath.Join(pkgsDir, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, packaging.PkgManifestFilename)); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func fixturesDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("fixtures: runtime.Caller failed")
	}
	return filepath.Dir(file)
}
