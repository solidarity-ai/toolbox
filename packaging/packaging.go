package packaging

import (
	"github.com/solidarity-ai/toolbox/packaging/internal/archive"
	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	"github.com/solidarity-ai/toolbox/packaging/internal/source"
)

// Type aliases for backward compatibility and convenience.
type LoadedPackage = source.LoadedPackage
type LoadDirResult = source.LoadDirResult
type ValidationMode = manifest.ValidationMode
type Warning = manifest.Warning
type PackResult = archive.PackResult

const (
	ValidationModeDev  = manifest.ValidationModeDev
	ValidationModeDist = manifest.ValidationModeDist

	DevManifestFilename = manifest.DevManifestFilename
	PkgManifestFilename = manifest.PkgManifestFilename
)

// LoadDev loads a source package from dir by reading toolbox.devpkg.json.
func LoadDev(dir string) (LoadedPackage, error) {
	return source.LoadDir(dir)
}

// LoadDevWithMode loads a source package using the given validation mode.
func LoadDevWithMode(dir string, mode ValidationMode) (LoadDirResult, error) {
	return source.LoadDirWithMode(dir, mode)
}

// Pack creates a .toolbox.pkg archive from the source package at dir.
func Pack(dir string, outDir string) (PackResult, error) {
	loaded, err := source.LoadDirWithMode(dir, manifest.ValidationModeDist)
	if err != nil {
		return PackResult{}, err
	}
	return archive.Pack(loaded.Loaded, outDir)
}

// LoadArchive loads a package from a .toolbox.pkg archive with its manifest.
func LoadArchive(archivePath, manifestPath string) (LoadedPackage, error) {
	return archive.LoadArchive(archivePath, manifestPath)
}
