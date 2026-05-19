package toolsetctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolpkgdiscovery"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

// SearchClient is the registry-backed package search surface used by the
// file-backed runtime toolset backend.
type SearchClient interface {
	SearchPackages(ctx context.Context, query registry.SearchQuery) (registry.PackageSearchResponse, error)
	SearchTools(ctx context.Context, query registry.SearchQuery) (registry.ToolSearchResponse, error)
}

type FileBackendOptions struct {
	ToolsetPath          string
	Resolver             *registry.Resolver
	Config               toolset.Config
	SearchClientFactory  func() (SearchClient, error)
	CredentialRepository *credentialrepo.Repository
	Consumer             PreparedToolConsumer
	AllowedEffects       map[tooldef.Effect]bool
}

// FileBackend persists runtime toolset mutations back to a toolset file and
// pushes the resulting effective prepared snapshot to one live consumer.
type FileBackend struct {
	mu                   sync.RWMutex
	toolsetPath          string
	resolver             *registry.Resolver
	config               toolset.Config
	searchClientFactory  func() (SearchClient, error)
	credentialRepository *credentialrepo.Repository
	consumer             PreparedToolConsumer
	allowedEffects       map[tooldef.Effect]bool
	prepared             *PreparedBackend
}

func NewFileBackend(ctx context.Context, opts FileBackendOptions) (*FileBackend, error) {
	path := strings.TrimSpace(opts.ToolsetPath)
	if path == "" {
		return nil, fmt.Errorf("file backend: toolset path is required")
	}

	backend := &FileBackend{
		toolsetPath:          path,
		resolver:             opts.Resolver,
		config:               opts.Config,
		searchClientFactory:  opts.SearchClientFactory,
		credentialRepository: opts.CredentialRepository,
		consumer:             opts.Consumer,
		allowedEffects:       cloneAllowedEffects(opts.AllowedEffects),
		prepared:             NewPreparedBackend(toolset.PreparedToolset{}, nil),
	}
	backend.prepared.SetBuiltinBackend(backend)
	if _, err := backend.reload(ctx); err != nil {
		return nil, err
	}
	return backend, nil
}

func (b *FileBackend) Prepared(ctx context.Context) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.preparedLocked(ctx)
}

func (b *FileBackend) preparedLocked(ctx context.Context) (toolset.PreparedToolset, error) {
	prepared, err := b.prepared.Prepared(ctx)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	return b.filterPreparedLocked(prepared), nil
}

func (b *FileBackend) filterPreparedLocked(prepared toolset.PreparedToolset) toolset.PreparedToolset {
	if len(b.allowedEffects) == 0 {
		return prepared
	}
	return prepared.FilterTools(func(tool toolset.PreparedTool) bool {
		return b.allowedEffects[tool.Effect]
	})
}

func (b *FileBackend) Reload(ctx context.Context) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, nil
	}
	return b.reload(ctx)
}

func (b *FileBackend) EnableToolsForPackageDiscovery() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.prepared.EnableToolsForPackageDiscovery()
}

func (b *FileBackend) EnableToolsForToolsetManagement() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.prepared.EnableToolsForToolsetManagement()
}

func (b *FileBackend) Search(ctx context.Context, req toolpkgdiscovery.SearchRequest) (toolpkgdiscovery.SearchResult, error) {
	if b == nil {
		return toolpkgdiscovery.SearchResult{}, unsupportedManagementOp("search")
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
	b.mu.RLock()
	factory := b.searchClientFactory
	b.mu.RUnlock()
	if factory == nil {
		return toolpkgdiscovery.SearchResult{}, fmt.Errorf("search: registry search is not configured")
	}

	client, err := factory()
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

func (b *FileBackend) Inspect(ctx context.Context, req toolpkgdiscovery.InspectRequest) (toolpkgdiscovery.InspectResult, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect: target is required")
	}

	b.mu.RLock()
	resolver := b.resolver
	toolsetPath := b.toolsetPath
	b.mu.RUnlock()

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
		cache, cacheErr := registry.NewCache("")
		if cacheErr == nil && cache.Has(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version)) {
			pkg, err := cache.LoadArchive(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
			if err != nil {
				return toolpkgdiscovery.InspectResult{}, err
			}
			return toolpkgdiscovery.InspectResult{
				Target:  target,
				Version: pkgVer.Version.String(),
				Source:  "cache",
				Package: pkg.Package,
			}, nil
		}
		if resolver == nil {
			return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect %s: resolver is not configured", target)
		}
		result, err := resolver.Resolve(ctx, registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		if err != nil {
			return toolpkgdiscovery.InspectResult{}, err
		}
		return toolpkgdiscovery.InspectResult{
			Target:  target,
			Version: pkgVer.Version.String(),
			Source:  string(result.Metadata.ResolvedFrom),
			Package: result.Package.Package,
		}, nil
	}

	ts, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		return toolpkgdiscovery.InspectResult{}, err
	}
	loaded, err := loadInstalledPackages(ctx, resolver, ts)
	if err != nil {
		return toolpkgdiscovery.InspectResult{}, err
	}
	if module, err := tooldef.ParseModulePath(target); err == nil {
		for _, pkg := range loaded.Packages.Packages {
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
	if pkg, ok := loaded.Packages.Package(target); ok {
		return toolpkgdiscovery.InspectResult{
			Target:  target,
			Version: pkg.Version.String(),
			Source:  "toolset",
			Package: pkg.Package.Package,
		}, nil
	}
	return toolpkgdiscovery.InspectResult{}, fmt.Errorf("inspect: target %q is not resolvable from %s", target, ts.SourceFilename())
}

func (b *FileBackend) Install(ctx context.Context, req InstallRequest) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, unsupportedManagementOp("install")
	}

	b.mu.Lock()
	var notify preparedToolNotification
	defer func() {
		b.mu.Unlock()
		notify.publish()
	}()

	module, version, err := b.resolveInstallPackageLocked(ctx, strings.TrimSpace(req.Package))
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	ts, err := toolsetfile.Load(b.toolsetPath)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	if err := ts.PutPackageVersion(module, version); err != nil {
		return toolset.PreparedToolset{}, err
	}
	if err := ts.Write(b.toolsetPath); err != nil {
		return toolset.PreparedToolset{}, err
	}
	prepared, notify, err := b.reloadLocked(ctx)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	return prepared, nil
}

func (b *FileBackend) Uninstall(ctx context.Context, req UninstallRequest) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, unsupportedManagementOp("uninstall")
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return toolset.PreparedToolset{}, fmt.Errorf("uninstall: target is required")
	}

	b.mu.Lock()
	var notify preparedToolNotification
	defer func() {
		b.mu.Unlock()
		notify.publish()
	}()

	ts, err := toolsetfile.Load(b.toolsetPath)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	module, err := resolveInstalledModule(ctx, b.resolver, ts, target)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	if err := ts.RemovePackage(module); err != nil {
		return toolset.PreparedToolset{}, err
	}
	if err := ts.Write(b.toolsetPath); err != nil {
		return toolset.PreparedToolset{}, err
	}
	prepared, notify, err := b.reloadLocked(ctx)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	return prepared, nil
}

func (b *FileBackend) Auth(ctx context.Context, req AuthRequest) (toolset.PreparedToolset, error) {
	if b == nil {
		return toolset.PreparedToolset{}, unsupportedManagementOp("auth")
	}

	b.mu.Lock()
	var notify preparedToolNotification
	defer func() {
		b.mu.Unlock()
		notify.publish()
	}()

	if b.credentialRepository == nil {
		return toolset.PreparedToolset{}, fmt.Errorf("auth: credential repository is not configured")
	}

	target := strings.TrimSpace(req.Target)
	if target == "" {
		return toolset.PreparedToolset{}, fmt.Errorf("auth: target is required")
	}

	renameRequested := strings.TrimSpace(req.RenameAccountFrom) != "" || strings.TrimSpace(req.RenameAccountTo) != ""
	actionCount := 0
	if req.Check {
		actionCount++
	}
	if renameRequested {
		actionCount++
	}
	if req.DeleteCredential {
		actionCount++
	}
	if strings.TrimSpace(req.DeleteAccount) != "" {
		actionCount++
	}
	if actionCount == 0 {
		return toolset.PreparedToolset{}, fmt.Errorf("auth: runtime auth only supports check, rename, delete account, or delete credential operations; use the CLI auth command to configure credentials")
	}
	if actionCount > 1 {
		return toolset.PreparedToolset{}, fmt.Errorf("auth: choose exactly one action")
	}

	loaded, err := b.loadInstalledPackageLocked(ctx, target)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	credentialName := strings.TrimSpace(req.Credential)
	switch {
	case req.Check:
		checks, err := b.credentialRepository.CheckPackage(ctx, loaded.Package)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		var missing []string
		for _, check := range checks.Credentials {
			if check.Configured() {
				continue
			}
			missing = append(missing, check.Credential.Name)
		}
		if len(missing) > 0 {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: package %s has unconfigured credentials: %s", loaded.Package.Name, strings.Join(missing, ", "))
		}
		return b.preparedLocked(ctx)
	case renameRequested:
		from := strings.TrimSpace(req.RenameAccountFrom)
		to := strings.TrimSpace(req.RenameAccountTo)
		if from == "" || to == "" {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: renameAccountFrom and renameAccountTo are both required")
		}
		if _, err := b.credentialRepository.RenameAccount(ctx, loaded.Package, credentialName, from, to); err != nil {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: %w", err)
		}
		var prepared toolset.PreparedToolset
		prepared, notify, err = b.reloadLocked(ctx)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		return prepared, nil
	case strings.TrimSpace(req.DeleteAccount) != "":
		if _, err := b.credentialRepository.DeleteAccount(ctx, loaded.Package, credentialName, strings.TrimSpace(req.DeleteAccount)); err != nil {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: %w", err)
		}
		var prepared toolset.PreparedToolset
		prepared, notify, err = b.reloadLocked(ctx)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		return prepared, nil
	case req.DeleteCredential:
		if credentialName == "" {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: credential is required when deleteCredential is true")
		}
		if _, err := b.credentialRepository.DeleteCredential(ctx, loaded.Package, credentialName); err != nil {
			return toolset.PreparedToolset{}, fmt.Errorf("auth: %w", err)
		}
		var prepared toolset.PreparedToolset
		prepared, notify, err = b.reloadLocked(ctx)
		if err != nil {
			return toolset.PreparedToolset{}, err
		}
		return prepared, nil
	default:
		return toolset.PreparedToolset{}, fmt.Errorf("auth: unsupported request")
	}
}

func (b *FileBackend) resolveInstallPackageLocked(ctx context.Context, spec string) (tooldef.ModulePath, tooldef.Version, error) {
	if spec == "" {
		return "", "", fmt.Errorf("install: package is required")
	}
	if strings.Contains(spec, "@") {
		pkgVer, err := tooldef.ParsePackageVer(spec)
		if err != nil {
			return "", "", fmt.Errorf("install package %q: %w", spec, err)
		}
		return pkgVer.Module, pkgVer.Version, nil
	}

	module, err := tooldef.ParseModulePath(spec)
	if err != nil {
		return "", "", fmt.Errorf("install package %q: %w", spec, err)
	}
	if b.resolver == nil {
		return "", "", fmt.Errorf("install %s: resolver is not configured", module)
	}
	versions, err := b.resolver.ListVersions(ctx, registry.ModulePath(module))
	if err != nil {
		return "", "", fmt.Errorf("install %s: %w", module, err)
	}
	if len(versions) == 0 {
		return "", "", fmt.Errorf("install %s: no versions found", module)
	}
	return module, tooldef.Version(versions[0]), nil
}

func (b *FileBackend) reload(ctx context.Context) (toolset.PreparedToolset, error) {
	b.mu.Lock()
	var notify preparedToolNotification
	defer func() {
		b.mu.Unlock()
		notify.publish()
	}()
	prepared, notify, err := b.reloadLocked(ctx)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	return prepared, nil
}

func (b *FileBackend) reloadLocked(ctx context.Context) (toolset.PreparedToolset, preparedToolNotification, error) {
	ts, err := toolsetfile.Load(b.toolsetPath)
	if err != nil {
		return toolset.PreparedToolset{}, preparedToolNotification{}, err
	}
	prepared, err := ts.Prepare(ctx, b.resolver, b.config)
	if err != nil {
		return toolset.PreparedToolset{}, preparedToolNotification{}, err
	}

	b.prepared.SetSnapshot(prepared, ts.AgentAllowsPackageDiscovery(), ts.AgentAllowsToolsetManagement())
	effective, err := b.preparedLocked(ctx)
	if err != nil {
		return toolset.PreparedToolset{}, preparedToolNotification{}, err
	}
	return effective, preparedToolNotification{consumer: b.consumer, prepared: effective}, nil
}

type preparedToolNotification struct {
	consumer PreparedToolConsumer
	prepared toolset.PreparedToolset
}

func (n preparedToolNotification) publish() {
	if n.consumer == nil {
		return
	}
	n.consumer.SetPreparedTools(n.prepared)
}

func (b *FileBackend) loadInstalledPackageLocked(ctx context.Context, target string) (packaging.LoadedPackage, error) {
	ts, err := toolsetfile.Load(b.toolsetPath)
	if err != nil {
		return packaging.LoadedPackage{}, err
	}
	loaded, err := loadInstalledPackages(ctx, b.resolver, ts)
	if err != nil {
		return packaging.LoadedPackage{}, err
	}
	if module, err := tooldef.ParseModulePath(target); err == nil {
		for _, pkg := range loaded.Packages.Packages {
			if pkg.Package.Package.Module == module {
				return pkg.Package, nil
			}
		}
	}
	if pkg, ok := loaded.Packages.Package(target); ok {
		return pkg.Package, nil
	}
	return packaging.LoadedPackage{}, fmt.Errorf("auth: target %q is not declared in %s", target, ts.SourceFilename())
}

func cloneAllowedEffects(in map[tooldef.Effect]bool) map[tooldef.Effect]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[tooldef.Effect]bool, len(in))
	for effect, allowed := range in {
		out[effect] = allowed
	}
	return out
}

type loadedToolsetContext struct {
	File     *toolsetfile.ToolsetFile
	Packages assembler.LoadedPackages
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
