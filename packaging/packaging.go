package packaging

import (
	"github.com/solidarity-ai/toolbox/packaging/internal/archive"
	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	"github.com/solidarity-ai/toolbox/packaging/internal/source"
	tooldef "github.com/solidarity-ai/toolbox/tool"
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
func Pack(dir string, outDir string, packerVersion tooldef.Version) (PackResult, error) {
	loaded, err := source.LoadDirWithMode(dir, manifest.ValidationModeDev)
	if err != nil {
		return PackResult{}, err
	}
	return archive.Pack(loaded.Loaded, outDir, packerVersion)
}

// LoadArchive loads a package from a .toolbox.pkg archive with its manifest.
func LoadArchive(archivePath, manifestPath string, check func(tooldef.Package) error) (LoadedPackage, error) {
	return archive.LoadArchive(archivePath, manifestPath, check)
}
