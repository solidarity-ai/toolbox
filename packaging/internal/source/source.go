package source

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// LoadedPackage is a fully loaded source package with its filesystem.
type LoadedPackage struct {
	Package tooldef.Package
	Files   fs.FS
	Dir     string
}

// LoadDir loads a source package from dir by reading toolbox.devpkg.json.
func LoadDir(dir string) (LoadedPackage, error) {
	result, err := LoadDirWithMode(dir, manifest.ValidationModeDev)
	if err != nil {
		return LoadedPackage{}, err
	}
	return result.Loaded, nil
}

// LoadDirResult combines validation results with the loaded package.
type LoadDirResult struct {
	Loaded   LoadedPackage
	Warnings []manifest.Warning
}

// LoadDirWithMode loads a source package using the given validation mode.
func LoadDirWithMode(dir string, mode manifest.ValidationMode) (LoadDirResult, error) {
	manifestPath := filepath.Join(dir, manifest.DevManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return LoadDirResult{}, err
	}

	dev, err := manifest.ParseDev(raw)
	if err != nil {
		return LoadDirResult{}, err
	}

	pkg := manifest.Compile(dev)

	files := NewSourceFS(os.DirFS(dir), dir, pkg)
	if err := EnrichToolMetadata(files, &pkg); err != nil {
		return LoadDirResult{}, err
	}

	warnings, err := manifest.ValidateCompiled(pkg, mode)
	if err != nil {
		return LoadDirResult{}, err
	}

	loaded := LoadedPackage{
		Package: pkg,
		Files:   files,
		Dir:     dir,
	}

	return LoadDirResult{
		Loaded:   loaded,
		Warnings: warnings,
	}, nil
}

// LoadBuiltDir loads a compiled package from dir by reading toolbox.pkg.json.
func LoadBuiltDir(dir string) (LoadedPackage, error) {
	return LoadBuiltDirWithMode(dir, manifest.ValidationModeDev)
}

// LoadBuiltDirWithMode loads a compiled package using the given validation mode.
func LoadBuiltDirWithMode(dir string, mode manifest.ValidationMode) (LoadedPackage, error) {
	compiledPath := filepath.Join(dir, manifest.PkgManifestFilename)
	raw, err := os.ReadFile(compiledPath)
	if err != nil {
		return LoadedPackage{}, err
	}

	pkg, err := manifest.ParsePkg(raw)
	if err != nil {
		return LoadedPackage{}, err
	}

	warnings, err := manifest.ValidateCompiled(pkg, mode)
	if err != nil {
		return LoadedPackage{}, err
	}
	_ = warnings

	files := NewSourceFS(os.DirFS(dir), dir, pkg)
	if err := EnrichToolMetadata(files, &pkg); err != nil {
		return LoadedPackage{}, err
	}

	return LoadedPackage{
		Package: pkg,
		Files:   files,
		Dir:     dir,
	}, nil
}

// NewSourceFS creates a filtered filesystem that only exposes files
// declared in the package manifest (tool entries + additional globs).
func NewSourceFS(base fs.FS, dir string, pkg tooldef.Package) fs.FS {
	files := allowedTypeScriptFiles(dir, pkg)
	dirs := allowedDirectories(files)
	return sourceFS{
		base:  base,
		files: files,
		dirs:  dirs,
	}
}

// EnrichToolMetadata extracts type signatures and JSDoc metadata from the
// tool source files and populates the package's tool definitions.
func EnrichToolMetadata(files fs.FS, pkg *tooldef.Package) error {
	for i := range pkg.Tools {
		tool := &pkg.Tools[i]
		meta, err := toolbox.ExtractToolMetadata(context.Background(), toolbox.ExtractInput{
			Files: files,
			Entry: tool.EntryTS,
		})
		if err != nil {
			return fmt.Errorf("extract tool metadata for %q: %w", tool.EntryTS, err)
		}
		if meta == nil || meta.Sig == nil {
			return fmt.Errorf("extract tool metadata for %q: missing function signature", tool.EntryTS)
		}
		if meta.Description != "" {
			tool.Description = meta.Description
		}
		tool.Sig = meta.Sig
		// Extract tool metadata from JSDoc tags (overrides manifest values).
		if meta.Sig != nil {
			for _, tag := range meta.Sig.Tags() {
				switch tag.Name {
				case "effect":
					if e := tooldef.Effect(tag.Text); e != "" {
						tool.Effect = e
					}
				case "idempotent":
					v := true
					tool.Idempotent = &v
				}
			}
		}
	}
	return nil
}
