package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type versionSources struct {
	Local    bool
	Registry bool
}

func runVersions(cmd versionsCmd, stdout io.Writer) error {
	cache, err := registry.NewCache("")
	if err != nil {
		return err
	}

	ctx := context.Background()
	module, parseErr := tooldef.ParseModulePath(cmd.Target)
	needResolver := cmd.Source != "local" || parseErr != nil

	var resolver *registry.Resolver
	if needResolver {
		resolver, err = newResolver()
		if err != nil {
			return err
		}
	}

	if parseErr != nil {
		module, err = resolveModuleForQuery(ctx, resolver, cmd.Toolset, cmd.Target)
		if err != nil {
			return err
		}
	}

	seen := map[tooldef.Version]versionSources{}
	if cmd.Source == "all" || cmd.Source == "local" {
		localVersions, err := cache.ListVersions(module)
		if err != nil {
			return err
		}
		for _, version := range localVersions {
			entry := seen[version]
			entry.Local = true
			seen[version] = entry
		}
	}
	if cmd.Source == "all" || cmd.Source == "registry" {
		registryVersions, err := resolver.ListVersions(ctx, module)
		if err != nil {
			if !(cmd.Source == "all" && errors.Is(err, registry.ErrReleaseNotFound)) {
				return err
			}
		} else {
			for _, version := range registryVersions {
				entry := seen[version]
				entry.Registry = true
				seen[version] = entry
			}
		}
	}

	if len(seen) == 0 {
		return fmt.Errorf("versions: no versions found for %s", module)
	}

	versions := make([]tooldef.Version, 0, len(seen))
	for version := range seen {
		versions = append(versions, version)
	}
	tooldef.SortVersionsDesc(versions)

	for _, version := range versions {
		if cmd.Source == "all" {
			fmt.Fprintf(stdout, "%s\t%s\n", version, formatVersionSources(seen[version]))
			continue
		}
		fmt.Fprintln(stdout, version)
	}
	return nil
}

func formatVersionSources(sources versionSources) string {
	switch {
	case sources.Local && sources.Registry:
		return "local,registry"
	case sources.Local:
		return "local"
	case sources.Registry:
		return "registry"
	default:
		return ""
	}
}
