package assembler

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// ErrNoResolver is returned when registry-backed package assembly is requested
// without providing a registry resolver.
var ErrNoResolver = errors.New("no registry resolver configured")

// Declaration is the file-agnostic assembly input for producing a prepared
// toolset from package references, replacements, and expected provenance.
type Declaration struct {
	Packages []PackageDeclaration
}

// PackageDeclaration declares one package to assemble into the toolset.
type PackageDeclaration struct {
	Module       tooldef.ModulePath
	Version      tooldef.Version
	ReplaceDir   string
	ArchivePath  string
	ManifestPath string
	Expected     *registry.ResolveMetadata
}

// LoadedPackage records one package materialized during declaration loading,
// along with the loaded tools derived from that package.
type LoadedPackage struct {
	Version  tooldef.Version
	Package  packaging.LoadedPackage
	Tools    []LoadedTool
	Local    bool
	Metadata *registry.ResolveMetadata
}

// LoadedPackages is the ordered set of packages materialized by Load.
type LoadedPackages struct {
	Packages []LoadedPackage
}

// Package returns the first loaded package with the given manifest package name.
func (pkgs LoadedPackages) Package(name string) (LoadedPackage, bool) {
	for _, pkg := range pkgs.Packages {
		if pkg.Package.Package.Name == name {
			return pkg, true
		}
	}
	return LoadedPackage{}, false
}

// Tool returns the first loaded tool in this package with the given tool name.
func (pkg LoadedPackage) Tool(name string) (LoadedTool, bool) {
	for _, tool := range pkg.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return LoadedTool{}, false
}

// Tools returns all loaded tools flattened in package order.
func (pkgs LoadedPackages) Tools() []LoadedTool {
	var tools []LoadedTool
	for _, pkg := range pkgs.Packages {
		tools = append(tools, pkg.Tools...)
	}
	return tools
}

// Load materializes all declared packages in deterministic module order.
func Load(ctx context.Context, resolver *registry.Resolver, decl Declaration) (LoadedPackages, error) {
	decls := make([]PackageDeclaration, len(decl.Packages))
	copy(decls, decl.Packages)
	sort.Slice(decls, func(i, j int) bool {
		return decls[i].Module.String() < decls[j].Module.String()
	})

	loadedPkgs := make([]LoadedPackage, 0, len(decls))
	for _, pkgDecl := range decls {
		packageKey := fmt.Sprintf("%s@%s", pkgDecl.Module, pkgDecl.Version)
		if pkgDecl.ReplaceDir != "" {
			pkg, err := packaging.LoadDev(pkgDecl.ReplaceDir)
			if err != nil {
				return LoadedPackages{}, fmt.Errorf("resolve %s from local replace %q: %w", packageKey, pkgDecl.ReplaceDir, err)
			}
			if err := validateLoadedModule(packageKey, pkgDecl.Module, pkg.Package.Module); err != nil {
				return LoadedPackages{}, err
			}
			loadedPkgs = append(loadedPkgs, LoadedPackage{
				Version: pkgDecl.Version,
				Package: pkg,
				Tools:   withPackageVersion(LoadedTools(pkg), pkgDecl.Version),
				Local:   true,
			})
			continue
		}
		if pkgDecl.ArchivePath != "" {
			pkg, err := packaging.LoadArchive(pkgDecl.ArchivePath, pkgDecl.ManifestPath)
			if err != nil {
				return LoadedPackages{}, fmt.Errorf("resolve %s from archive %q: %w", packageKey, pkgDecl.ArchivePath, err)
			}
			if err := validateLoadedModule(packageKey, pkgDecl.Module, pkg.Package.Module); err != nil {
				return LoadedPackages{}, err
			}
			loadedPkgs = append(loadedPkgs, LoadedPackage{
				Version: pkgDecl.Version,
				Package: pkg,
				Tools:   withPackageVersion(LoadedTools(pkg), pkgDecl.Version),
				Local:   true,
			})
			continue
		}

		if resolver == nil {
			return LoadedPackages{}, ErrNoResolver
		}

		result, err := resolver.ResolveWithExpected(ctx, registry.ModulePath(pkgDecl.Module), registry.Version(pkgDecl.Version), pkgDecl.Expected)
		if err != nil {
			return LoadedPackages{}, err
		}
		metadata := result.Metadata
		if err := validateLoadedModule(packageKey, pkgDecl.Module, result.Package.Package.Module); err != nil {
			return LoadedPackages{}, err
		}
		loadedPkgs = append(loadedPkgs, LoadedPackage{
			Version:  pkgDecl.Version,
			Package:  result.Package,
			Tools:    withPackageVersion(LoadedTools(result.Package), pkgDecl.Version),
			Metadata: &metadata,
		})
	}

	return LoadedPackages{Packages: loadedPkgs}, nil
}

func withPackageVersion(tools []LoadedTool, version tooldef.Version) []LoadedTool {
	for i := range tools {
		tools[i].PackageVersion = version
	}
	return tools
}

func validateLoadedModule(packageKey string, declared, actual tooldef.ModulePath) error {
	if declared == actual {
		return nil
	}
	return fmt.Errorf("resolve %s: package manifest module %q does not match declared module %q", packageKey, actual, declared)
}
