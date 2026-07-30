package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

type packageInfoView struct {
	Target  string          `json:"target,omitempty"`
	Version string          `json:"version,omitempty"`
	Source  string          `json:"source"`
	Package tooldef.Package `json:"package"`
}

func runInfo(cmd infoCmd, stdout io.Writer) error {
	cache, err := registry.NewCache("")
	if err != nil {
		return err
	}
	resolver, err := newResolver()
	if err != nil {
		return err
	}

	ctx := context.Background()
	view, err := loadInfoView(ctx, cache, resolver, cmd.Toolset, cmd.Target)
	if err != nil {
		return err
	}

	if cmd.JSON {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return nil
	}

	fmt.Fprintf(stdout, "target: %s\n", view.Target)
	if view.Version != "" {
		fmt.Fprintf(stdout, "version: %s\n", view.Version)
	}
	fmt.Fprintf(stdout, "source: %s\n", view.Source)
	fmt.Fprintf(stdout, "module: %s\n", view.Package.Module)
	fmt.Fprintf(stdout, "name: %s\n", view.Package.Name)
	fmt.Fprintf(stdout, "runtime: %s\n", view.Package.Runtime)
	fmt.Fprintf(stdout, "tools: %d\n", len(view.Package.Tools))
	for _, tool := range view.Package.Tools {
		fmt.Fprintf(stdout, "  - %s\n", tool.EntryTS)
	}
	if len(view.Package.Credentials) > 0 {
		fmt.Fprintf(stdout, "credentials: %d\n", len(view.Package.Credentials))
		for _, credential := range view.Package.Credentials {
			fmt.Fprintf(stdout, "  - %s (%s)\n", credential.Name, credential.Type)
		}
	}
	if len(view.Package.AllowedHosts) > 0 {
		fmt.Fprintln(stdout, "allowed hosts:")
		for _, host := range view.Package.AllowedHosts {
			fmt.Fprintf(stdout, "  - %s\n", host)
		}
	}
	return nil
}

func loadInfoView(ctx context.Context, cache *registry.Cache, resolver *registry.Resolver, toolsetPath, target string) (packageInfoView, error) {
	if stat, err := os.Stat(target); err == nil && stat.IsDir() {
		pkg, err := packaging.LoadDev(target)
		if err != nil {
			return packageInfoView{}, err
		}
		return packageInfoView{
			Target:  target,
			Source:  "local-dir",
			Package: pkg.Package,
		}, nil
	}

	if pkgVer, err := tooldef.ParsePackageVer(target); err == nil {
		cached := cache.Has(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		result, err := resolver.Resolve(ctx, registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		if err != nil {
			return packageInfoView{}, err
		}
		source := string(result.Metadata.ResolvedFrom)
		if cached {
			source = "cache"
		}
		return packageInfoView{
			Target:  target,
			Version: pkgVer.Version.String(),
			Source:  source,
			Package: result.Package.Package,
		}, nil
	}

	ts, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		return packageInfoView{}, err
	}
	loaded, err := loadInstalledPackages(ctx, resolver, ts)
	if err != nil {
		return packageInfoView{}, err
	}
	if module, err := tooldef.ParseModulePath(target); err == nil {
		for _, pkg := range loaded.Packages.Packages {
			if pkg.Package.Package.Module == module {
				return packageInfoView{
					Target:  target,
					Version: pkg.Version.String(),
					Source:  "toolset",
					Package: pkg.Package.Package,
				}, nil
			}
		}
	}
	if pkg, ok := loaded.Packages.Package(target); ok {
		return packageInfoView{
			Target:  target,
			Version: pkg.Version.String(),
			Source:  "toolset",
			Package: pkg.Package.Package,
		}, nil
	}
	return packageInfoView{}, fmt.Errorf("info: target %q is not resolvable from %s", target, ts.SourceFilename())
}
