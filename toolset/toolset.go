package toolset

import (
	"context"
	"errors"
	"fmt"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// ErrNoResolver is returned by AddFromRegistry when the Builder was created
// without a registry resolver.
var ErrNoResolver = errors.New("no registry resolver configured")

// ResolvedToolset is a stubbed resolved toolset for the first outside-in tests.
//
// For now it only carries the visible tools for one request.
type ResolvedToolset struct {
	tools []tooldef.ResolvedTool
}

// Builder incrementally assembles a toolset from source package directories.
//
// For now it only records loaded packages from
// toolbox.devpkg.json. Tool selection and binding come later.
type Builder struct {
	packages []packaging.LoadedPackage
	resolver *registry.Resolver
}

// New creates an empty toolset builder.
func New() *Builder {
	return &Builder{}
}

// NewWithResolver creates a toolset builder that can resolve registry packages.
func NewWithResolver(resolver *registry.Resolver) *Builder {
	return &Builder{resolver: resolver}
}

// AddFromDir loads a package rooted at dir.
//
// A directory is treated as a package iff it contains toolbox.devpkg.json.
func (b *Builder) AddFromDir(dir string) error {
	pkg, err := packaging.LoadDev(dir)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// AddFromArchive loads a package from a .toolbox.pkg archive and its manifest.
func (b *Builder) AddFromArchive(archivePath, manifestPath string) error {
	pkg, err := packaging.LoadArchive(archivePath, manifestPath)
	if err != nil {
		return err
	}
	b.packages = append(b.packages, pkg)
	return nil
}

// AddFromRegistry resolves a registry package by module path and version,
// then appends it to the builder's package list.
func (b *Builder) AddFromRegistry(ctx context.Context, modulePath, version string) error {
	_, err := b.AddFromRegistryWithExpected(ctx, modulePath, version, nil)
	return err
}

// AddFromRegistryWithExpected resolves a registry package through the shared
// resolver path, optionally verifying cached/fetched bytes against expected
// lock metadata, then appends the loaded package and returns the metadata that
// was trusted for this package.
func (b *Builder) AddFromRegistryWithExpected(ctx context.Context, modulePath, version string, expected *registry.ResolveMetadata) (registry.ResolveMetadata, error) {
	if b.resolver == nil {
		return registry.ResolveMetadata{}, ErrNoResolver
	}

	module, err := tooldef.ParseModulePath(modulePath)
	if err != nil {
		return registry.ResolveMetadata{}, fmt.Errorf("parse module path: %w", err)
	}
	ver, err := tooldef.ParseVersion(version)
	if err != nil {
		return registry.ResolveMetadata{}, fmt.Errorf("parse version: %w", err)
	}

	result, err := b.resolver.ResolveWithExpected(ctx, module, ver, expected)
	if err != nil {
		return registry.ResolveMetadata{}, err
	}
	b.packages = append(b.packages, result.Package)
	return result.Metadata, nil
}

// Packages returns the currently loaded source packages.
func (b *Builder) Packages() []tooldef.Package {
	out := make([]tooldef.Package, len(b.packages))
	for i, loaded := range b.packages {
		out[i] = loaded.Package
	}
	return out
}

// Resolve materializes one visible tool per loaded package tool.
func (b *Builder) Resolve() ResolvedToolset {
	var tools []tooldef.ResolvedTool
	for _, loaded := range b.packages {
		tools = append(tools, loaded.ResolvedTools()...)
	}
	return NewResolvedToolset(tools)
}

// NewResolvedToolset creates a resolved toolset from a visible tool list.
func NewResolvedToolset(tools []tooldef.ResolvedTool) ResolvedToolset {
	out := make([]tooldef.ResolvedTool, len(tools))
	copy(out, tools)
	return ResolvedToolset{tools: out}
}

// Tools returns a shallow copy of the visible tools for this resolved toolset.
func (r ResolvedToolset) Tools() []tooldef.ResolvedTool {
	out := make([]tooldef.ResolvedTool, len(r.tools))
	copy(out, r.tools)
	return out
}
