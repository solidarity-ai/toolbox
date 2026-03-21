package source

import (
	"context"
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

	warnings, err := manifest.ValidateCompiled(pkg, mode)
	if err != nil {
		return LoadDirResult{}, err
	}

	files := NewSourceFS(os.DirFS(dir), dir, pkg)
	enrichToolMetadata(files, &pkg)

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

// ResolvedTools produces resolved tool definitions from the loaded package.
func (p LoadedPackage) ResolvedTools() []tooldef.ResolvedTool {
	tools := make([]tooldef.ResolvedTool, 0, len(p.Package.Tools))
	for _, pkgTool := range p.Package.Tools {
		description := pkgTool.Description
		if description == "" {
			description = manifest.InferToolName(pkgTool.EntryTS)
		}
		resolved := tooldef.ResolvedTool{
			Name:         manifest.InferToolName(pkgTool.EntryTS),
			Description:  description,
			ParamsSchema: pkgTool.ParamsSchema,
			Package:      &p.Package,
		}

		baseDef := tooldef.TSToolDef{
			Entry:       pkgTool.EntryTS,
			Files:       p.Files,
			PackageRoot: p.Dir,
		}

		if p.Package.Runtime == tooldef.RuntimeTypeScriptWasixSandbox {
			resolved.TSWasm = &tooldef.TSWasmToolDef{
				TSToolDef:   baseDef,
				Executables: p.Package.Executables,
			}
		} else {
			resolved.TS = &baseDef
		}

		tools = append(tools, resolved)
	}
	return tools
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

	return LoadedPackage{
		Package: pkg,
		Files:   NewSourceFS(os.DirFS(dir), dir, pkg),
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

func enrichToolMetadata(files fs.FS, pkg *tooldef.Package) {
	for i := range pkg.Tools {
		tool := &pkg.Tools[i]
		meta, err := toolbox.ExtractToolMetadata(context.Background(), toolbox.ExtractInput{
			Files: files,
			Entry: tool.EntryTS,
		})
		if err != nil {
			continue
		}
		if meta.Description != "" {
			tool.Description = meta.Description
		}
		if meta.ParamsSchema != nil {
			tool.ParamsSchema = meta.ParamsSchema
		}
	}
}
