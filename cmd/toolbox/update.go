package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/solidarity-ai/toolbox/registry"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func runUpdate(cmd updateCmd, stdout io.Writer) error {
	if (cmd.Target == "") == !cmd.All {
		return fmt.Errorf("update: provide either <target> or --all")
	}
	if cmd.Patch && cmd.Minor {
		return fmt.Errorf("update: --patch and --minor are mutually exclusive")
	}

	resolver, err := newResolver()
	if err != nil {
		return err
	}

	ts, err := toolsetfile.Load(cmd.Toolset)
	if err != nil {
		return err
	}

	ctx := context.Background()
	modules, err := updateModulesForTarget(ctx, resolver, ts, cmd)
	if err != nil {
		return err
	}

	changed := 0
	for _, module := range modules {
		current, err := tooldef.ParseVersion(ts.Packages[module.String()])
		if err != nil {
			return fmt.Errorf("update %s: parse current version: %w", module, err)
		}
		remoteVersions, err := resolver.ListVersions(ctx, module)
		if err != nil {
			return err
		}
		next, ok, err := selectUpdateVersion(current, remoteVersions, cmd.Patch, cmd.Minor)
		if err != nil {
			return fmt.Errorf("update %s: %w", module, err)
		}
		if !ok {
			fmt.Fprintf(stdout, "update %s: already at %s\n", module, current)
			continue
		}
		if err := ts.SetPackageVersion(module, next); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "updated %s: %s -> %s\n", module, current, next)
		changed++
	}
	if changed > 0 {
		if err := ts.Write(ts.SourceFilename()); err != nil {
			return err
		}
	}

	prepared, err := ts.Prepare(ctx, resolver)
	if err != nil {
		return err
	}
	if changed == 0 {
		fmt.Fprintln(stdout, "no packages changed")
	}
	fmt.Fprintf(stdout, "resolved %d tools\n", len(prepared.Tools()))
	if ts.LockFilename() != "" {
		fmt.Fprintf(stdout, "lockfile: %s\n", ts.LockFilename())
	}
	return nil
}

func updateModulesForTarget(ctx context.Context, resolver *registry.Resolver, ts *toolsetfile.ToolsetFile, cmd updateCmd) ([]tooldef.ModulePath, error) {
	if cmd.All {
		return sortedToolsetModules(ts), nil
	}
	module, err := resolveInstalledModule(ctx, resolver, ts, cmd.Target)
	if err != nil {
		return nil, err
	}
	return []tooldef.ModulePath{module}, nil
}

func runOutdated(cmd outdatedCmd, stdout io.Writer) error {
	resolver, err := newResolver()
	if err != nil {
		return err
	}

	ts, err := toolsetfile.Load(cmd.Toolset)
	if err != nil {
		return err
	}

	type row struct {
		module  tooldef.ModulePath
		current tooldef.Version
		latest  tooldef.Version
	}

	ctx := context.Background()
	rows := make([]row, 0, len(ts.Packages))
	for _, module := range sortedToolsetModules(ts) {
		current, err := tooldef.ParseVersion(ts.Packages[module.String()])
		if err != nil {
			return fmt.Errorf("outdated %s: parse current version: %w", module, err)
		}
		versions, err := resolver.ListVersions(ctx, module)
		if err != nil {
			if errors.Is(err, registry.ErrReleaseNotFound) {
				continue
			}
			return err
		}
		if len(versions) == 0 {
			continue
		}
		latest := versions[0]
		if compareVersions(latest, current) <= 0 {
			continue
		}
		rows = append(rows, row{module: module, current: current, latest: latest})
	}

	if len(rows) == 0 {
		fmt.Fprintln(stdout, "all packages up to date")
		return nil
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODULE\tCURRENT\tLATEST")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", row.module, row.current, row.latest)
	}
	return tw.Flush()
}

func selectUpdateVersion(current tooldef.Version, versions []tooldef.Version, patchOnly, minorOnly bool) (tooldef.Version, bool, error) {
	currentSemver, currentOK := parseSemver(current)
	if (patchOnly || minorOnly) && !currentOK {
		return "", false, fmt.Errorf("current version %s is not a strict semver, so --patch/--minor cannot be applied", current)
	}
	for _, candidate := range versions {
		if compareVersions(candidate, current) <= 0 {
			continue
		}
		if patchOnly || minorOnly {
			candidateSemver, ok := parseSemver(candidate)
			if !ok {
				continue
			}
			if candidateSemver.major != currentSemver.major {
				continue
			}
			if patchOnly && candidateSemver.minor != currentSemver.minor {
				continue
			}
		}
		return candidate, true, nil
	}
	return "", false, nil
}
