package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

type loadedToolsetContext struct {
	File     *toolsetfile.ToolsetFile
	Packages assembler.LoadedPackages
}

func resolveModuleForQuery(ctx context.Context, resolver *registry.Resolver, toolsetPath, target string) (tooldef.ModulePath, error) {
	module, parseErr := tooldef.ParseModulePath(target)
	if parseErr == nil {
		return module, nil
	}
	ts, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		return "", fmt.Errorf("target %q is not a module path (%v) and toolset %s could not be loaded to resolve installed targets: %w", target, parseErr, toolsetPath, err)
	}
	return resolveInstalledModule(ctx, resolver, ts, target)
}

func resolveInstalledModule(ctx context.Context, resolver *registry.Resolver, ts *toolsetfile.ToolsetFile, target string) (tooldef.ModulePath, error) {
	if module, err := tooldef.ParseModulePath(target); err == nil {
		if _, ok := ts.Packages[module.String()]; ok {
			return module, nil
		}
		return "", fmt.Errorf("target %q is not declared in %s", target, ts.SourceFilename())
	}

	loaded, err := loadInstalledPackages(ctx, resolver, ts)
	if err != nil {
		return "", err
	}
	if pkg, ok := loaded.Packages.Package(target); ok {
		return pkg.Package.Package.Module, nil
	}
	return "", fmt.Errorf("target %q is not declared in %s", target, ts.SourceFilename())
}

func loadInstalledPackages(ctx context.Context, resolver *registry.Resolver, ts *toolsetfile.ToolsetFile) (*loadedToolsetContext, error) {
	var existingLock *toolsetfile.ToolsetLockFile
	if ts.LockFilename() != "" {
		lock, err := toolsetfile.LoadLock(ts.LockFilename())
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			existingLock = lock
		}
	}
	if existingLock == nil {
		existingLock = &toolsetfile.ToolsetLockFile{Packages: map[string]toolsetfile.ToolsetLockEntry{}}
	}

	local, err := ts.LoadLocal()
	if err != nil {
		return nil, err
	}
	decl, err := ts.Declaration(local, existingLock)
	if err != nil {
		return nil, err
	}
	loaded, err := assembler.Load(ctx, resolver, decl)
	if err != nil {
		return nil, err
	}
	if err := validateUniqueLoadedPackageNames(loaded); err != nil {
		return nil, err
	}
	return &loadedToolsetContext{File: ts, Packages: loaded}, nil
}

func validateUniqueLoadedPackageNames(loaded assembler.LoadedPackages) error {
	seen := make(map[string]tooldef.ModulePath, len(loaded.Packages))
	for _, pkg := range loaded.Packages {
		name := pkg.Package.Package.Name
		module := pkg.Package.Package.Module
		if first, ok := seen[name]; ok {
			return fmt.Errorf("package name %q is declared by both %q and %q; duplicate package names in one toolset require aliases", name, first, module)
		}
		seen[name] = module
	}
	return nil
}

func sortedToolsetModules(ts *toolsetfile.ToolsetFile) []tooldef.ModulePath {
	modules := make([]tooldef.ModulePath, 0, len(ts.Packages))
	for rawModule := range ts.Packages {
		module, err := tooldef.ParseModulePath(rawModule)
		if err != nil {
			continue
		}
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].String() < modules[j].String()
	})
	return modules
}
