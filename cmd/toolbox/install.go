package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func runInstall(cmd installCmd, stdout io.Writer) error {
	resolver, err := newResolver()
	if err != nil {
		return err
	}

	ctx := context.Background()
	ts, created, err := loadToolsetForInstall(cmd.Toolset, cmd.Package != "")
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(stdout, "created toolset file: %s\n", ts.SourceFilename())
	}
	if cmd.Package != "" {
		module, version, err := resolveInstallPackage(ctx, resolver, cmd.Package)
		if err != nil {
			return err
		}

		previous := ts.Packages[module.String()]
		if err := ts.PutPackageVersion(module, version); err != nil {
			return err
		}
		if err := ts.Write(cmd.Toolset); err != nil {
			return err
		}

		switch {
		case previous == "":
			fmt.Fprintf(stdout, "installed %s@%s\n", module, version)
		case previous == version.String():
			fmt.Fprintf(stdout, "already declared %s@%s\n", module, version)
		default:
			fmt.Fprintf(stdout, "installed %s: %s -> %s\n", module, previous, version)
		}
	}

	prepared, err := ts.Prepare(ctx, resolver)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "resolved %d tools\n", len(prepared.Tools()))
	if ts.LockFilename() != "" {
		fmt.Fprintf(stdout, "lockfile: %s\n", ts.LockFilename())
	}
	return nil
}

func loadToolsetForInstall(path string, allowCreate bool) (*toolsetfile.ToolsetFile, bool, error) {
	ts, err := toolsetfile.Load(path)
	if err == nil {
		return ts, false, nil
	}
	if !allowCreate || !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	ts, err = toolsetfile.Parse([]byte("{\n  \"packages\": {},\n  \"tools\": []\n}\n"))
	if err != nil {
		return nil, false, fmt.Errorf("create toolset file %q: %w", path, err)
	}
	if err := ts.Write(path); err != nil {
		return nil, false, err
	}
	return ts, true, nil
}

func resolveInstallPackage(ctx context.Context, resolver *registry.Resolver, spec string) (tooldef.ModulePath, tooldef.Version, error) {
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
	versions, err := resolver.ListVersions(ctx, module)
	if err != nil {
		return "", "", fmt.Errorf("install %s: %w", module, err)
	}
	if len(versions) == 0 {
		return "", "", fmt.Errorf("install %s: no versions found", module)
	}
	return module, versions[0], nil
}
