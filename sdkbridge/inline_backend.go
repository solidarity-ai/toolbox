package sdkbridge

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolpkgdiscovery"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

type inlineBackendOptions struct {
	File                *toolsetfile.ToolsetFile
	Resolver            *registry.Resolver
	Config              toolset.Config
	SearchClientFactory func() (toolsetctl.SearchClient, error)
	Consumer            toolsetctl.PreparedToolConsumer
}

// inlineBackend gives sdkbridge inline toolset.compose calls the same read-only
// discovery surface as file-backed toolsets while still rejecting runtime
// installation and auth operations that require a mutable source of truth.
type inlineBackend struct {
	file                *toolsetfile.ToolsetFile
	resolver            *registry.Resolver
	searchClientFactory func() (toolsetctl.SearchClient, error)
	prepared            *toolsetctl.PreparedBackend
}

func newInlineBackend(ctx context.Context, opts inlineBackendOptions) (*inlineBackend, error) {
	if opts.File == nil {
		return nil, fmt.Errorf("inline backend: toolset is required")
	}

	prepared, err := opts.File.Prepare(ctx, opts.Resolver, opts.Config)
	if err != nil {
		return nil, err
	}

	backend := &inlineBackend{
		file:                opts.File,
		resolver:            opts.Resolver,
		searchClientFactory: opts.SearchClientFactory,
	}
	backend.prepared = toolsetctl.NewPreparedBackend(prepared, opts.Consumer)
	backend.prepared.SetBuiltinBackend(backend)
	backend.prepared.SetSnapshot(prepared, opts.File.AgentAllowsPackageDiscovery(), false)
	return backend, nil
}

func (b *inlineBackend) Prepared(ctx context.Context) (toolset.PreparedToolset, error) {
	if b == nil || b.prepared == nil {
		return toolset.PreparedToolset{}, nil
	}
	return b.prepared.Prepared(ctx)
}

func (b *inlineBackend) EnableToolsForPackageDiscovery() bool {
	if b == nil || b.prepared == nil {
		return false
	}
	return b.prepared.EnableToolsForPackageDiscovery()
}

func (b *inlineBackend) EnableToolsForToolsetManagement() bool {
	if b == nil || b.prepared == nil {
		return false
	}
	return b.prepared.EnableToolsForToolsetManagement()
}

func (b *inlineBackend) Search(ctx context.Context, req toolpkgdiscovery.SearchRequest) (toolpkgdiscovery.SearchResult, error) {
	if b == nil {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: inline backend is not available")
	}
	if strings.TrimSpace(req.Query) == "" {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: query must not be empty")
	}
	if req.Tools && req.Packages {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: tools and packages are mutually exclusive")
	}
	if req.Limit < 0 {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: limit must be zero or greater")
	}
	if req.Offset < 0 {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: offset must be zero or greater")
	}
	if req.Limit == 0 {
		req.Limit = 20
	}
	if !req.Tools && !req.Packages {
		req.Packages = true
	}
	if b.searchClientFactory == nil {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: registry search is not configured")
	}

	client, err := b.searchClientFactory()
	if err != nil {
		return toolpkgdiscovery.SearchResult{}, err
	}

	query := registry.SearchQuery{
		Q:       strings.TrimSpace(req.Query),
		Runtime: strings.TrimSpace(req.Runtime),
		Effect:  strings.TrimSpace(req.Effect),
		Limit:   req.Limit,
		Offset:  req.Offset,
	}
	if req.Tools {
		response, err := client.SearchTools(ctx, query)
		if err != nil {
			return toolpkgdiscovery.SearchResult{}, err
		}
		return toolpkgdiscovery.SearchResult{Tools: response.Hits}, nil
	}

	response, err := client.SearchPackages(ctx, query)
	if err != nil {
		return toolpkgdiscovery.SearchResult{}, err
	}
	return toolpkgdiscovery.SearchResult{Packages: response.Hits}, nil
}

func (b *inlineBackend) Inspect(ctx context.Context, req toolpkgdiscovery.InspectRequest) (toolpkgdiscovery.InspectResult, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect: target is required")
	}

	if stat, err := os.Stat(target); err == nil && stat.IsDir() {
		pkg, err := packaging.LoadDev(target)
		if err != nil {
			return toolpkgdiscovery.InspectResult{}, err
		}
		return toolpkgdiscovery.InspectResult{
			Target:  target,
			Source:  "local-dir",
			Package: pkg.Package,
		}, nil
	}

	if pkgVer, err := tooldef.ParsePackageVer(target); err == nil {
		if b.resolver == nil {
			return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect %s: resolver is not configured", target)
		}
		cache, cacheErr := registry.NewCache("")
		cached := cacheErr == nil && cache.Has(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		result, err := b.resolver.Resolve(ctx, registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		if err != nil {
			return toolpkgdiscovery.InspectResult{}, err
		}
		source := string(result.Metadata.ResolvedFrom)
		if cached {
			source = "cache"
		}
		return toolpkgdiscovery.InspectResult{
			Target:  target,
			Version: pkgVer.Version.String(),
			Source:  source,
			Package: result.Package.Package,
		}, nil
	}

	loaded, err := b.loadInstalledPackages(ctx)
	if err != nil {
		return toolpkgdiscovery.InspectResult{}, err
	}
	if module, err := tooldef.ParseModulePath(target); err == nil {
		for _, pkg := range loaded.Packages {
			if pkg.Package.Package.Module != module {
				continue
			}
			return toolpkgdiscovery.InspectResult{
				Target:  target,
				Version: pkg.Version.String(),
				Source:  "toolset",
				Package: pkg.Package.Package,
			}, nil
		}
	}
	if pkg, ok := loaded.Package(target); ok {
		return toolpkgdiscovery.InspectResult{
			Target:  target,
			Version: pkg.Version.String(),
			Source:  "toolset",
			Package: pkg.Package.Package,
		}, nil
	}
	return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect: target %q is not resolvable from inline toolset", target)
}

func (*inlineBackend) Install(context.Context, toolsetctl.InstallRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedInlineMutation("install")
}

func (*inlineBackend) Uninstall(context.Context, toolsetctl.UninstallRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedInlineMutation("uninstall")
}

func (*inlineBackend) Auth(context.Context, toolsetctl.AuthRequest) (toolset.PreparedToolset, error) {
	return toolset.PreparedToolset{}, unsupportedInlineMutation("auth")
}

func (b *inlineBackend) loadInstalledPackages(ctx context.Context) (assembler.LoadedPackages, error) {
	if b == nil || b.file == nil {
		return assembler.LoadedPackages{}, fmt.Errorf("inline backend: toolset is not available")
	}
	decl, err := b.file.Declaration(nil, nil)
	if err != nil {
		return assembler.LoadedPackages{}, err
	}
	return assembler.Load(ctx, b.resolver, decl)
}

func unsupportedInlineMutation(name string) error {
	return fmt.Errorf("%s: %w", name, toolsetctl.ErrManagementToolsUnsupported)
}
