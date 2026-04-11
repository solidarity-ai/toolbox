package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/alecthomas/kong"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/sdkbridge"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

const (
	defaultToolsetFilename     = "toolbox.toolset.json"
	defaultToolRegistryBaseURL = "https://packages.include.tools"
	defaultToolRegistryTimeout = 10 * time.Second
)

type cli struct {
	Install   installCmd   `cmd:"" help:"Resolve the selected toolset and write or update its lockfile."`
	Update    updateCmd    `cmd:"" help:"Update one installed package or all installed packages in the selected toolset."`
	Versions  versionsCmd  `cmd:"" help:"List cached and published versions for a package target."`
	Info      infoCmd      `cmd:"" help:"Show package manifest information for an installed target, local dir, or explicit package version."`
	Outdated  outdatedCmd  `cmd:"" help:"Show installed packages in the selected toolset with newer published versions."`
	MCP       mcpCmd       `cmd:"" name:"mcp" help:"Serve the selected toolset over MCP stdio."`
	Auth      authCmd      `cmd:"" help:"Legacy auth surface. This command is intentionally left on the existing parser while the auth CLI redesign is finalized."`
	SDKBridge sdkBridgeCmd `cmd:"" name:"_sdkbridge" hidden:"" help:"Internal SDK bridge commands."`
}

type installCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to resolve."`
	Package string `arg:"" optional:"" name:"package" help:"Package module path or module@version to declare before resolving."`
}

func (installCmd) Help() string {
	return `Without <package>, install resolves the selected toolset and updates its lockfile.

With <package>, install first updates the toolset file, then resolves it.

Use <module> to install the latest available version.
Use <module>@<version> to install that exact version.

For a specific commit, pass the Go pseudo-version directly, for example
<module>@v0.0.0-20260410153000-abcdef123456.

If the selected toolset file does not exist, install creates a default empty
toolset file before adding the package.`
}

type updateCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to update."`
	All     bool   `help:"Update all installed packages in the selected toolset."`
	Patch   bool   `help:"Limit updates to patch releases for semver packages."`
	Minor   bool   `help:"Limit updates to minor releases for semver packages."`
	Target  string `arg:"" optional:"" name:"target" help:"Installed package target to update."`
}

type versionsCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file used to resolve non-module targets."`
	Source  string `default:"all" enum:"all,local,registry" help:"Version sources to include."`
	Target  string `arg:"" name:"target" help:"Module path or installed package target."`
}

type infoCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file used to resolve non-version targets."`
	JSON    bool   `help:"Emit structured JSON output."`
	Target  string `arg:"" name:"target" help:"Installed target, local package dir, or package@version."`
}

type outdatedCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to inspect."`
}

type mcpCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to serve."`
}

type authCmd struct {
	Args []string `arg:"" optional:"" passthrough:"all" name:"arg" help:"Legacy auth arguments."`
}

type sdkBridgeCmd struct {
	ServeStdio sdkBridgeServeStdioCmd `cmd:"" name:"serve-stdio" hidden:"" help:"Serve the internal SDK bridge over stdio."`
}

type sdkBridgeServeStdioCmd struct{}

type loadedToolsetContext struct {
	File     *toolsetfile.ToolsetFile
	Packages assembler.LoadedPackages
}

type packageInfoView struct {
	Target  string          `json:"target,omitempty"`
	Version string          `json:"version,omitempty"`
	Source  string          `json:"source"`
	Package tooldef.Package `json:"package"`
}

type versionSources struct {
	Local    bool
	Registry bool
}

func main() {
	if err := runWithIO(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithIO(args, os.Stdin, stdout, stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var parsed cli
	parser, err := kong.New(
		&parsed,
		kong.Name("toolbox"),
		kong.Description("Toolbox resolves toolsets, inspects package metadata, and serves tools over MCP."),
		kong.Writers(stdout, stderr),
	)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		ctx, err := kong.Trace(parser, nil)
		if err != nil {
			return err
		}
		return ctx.PrintUsage(false)
	}

	ctx, err := parser.Parse(args)
	if err != nil {
		return err
	}

	command := ctx.Command()
	switch {
	case strings.HasPrefix(command, "install"):
		return runInstall(parsed.Install, stdout)
	case strings.HasPrefix(command, "update"):
		return runUpdate(parsed.Update, stdout)
	case strings.HasPrefix(command, "versions"):
		return runVersions(parsed.Versions, stdout)
	case strings.HasPrefix(command, "info"):
		return runInfo(parsed.Info, stdout)
	case strings.HasPrefix(command, "outdated"):
		return runOutdated(parsed.Outdated, stdout)
	case strings.HasPrefix(command, "mcp"):
		return runMCP(parsed.MCP, stdin, stdout, stderr)
	case strings.HasPrefix(command, "auth"):
		return runAuth(parsed.Auth.Args, stdin, stdout, stderr)
	case strings.HasPrefix(command, "_sdkbridge serve-stdio"):
		return runSDKBridgeServeStdio(stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

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
	sortVersionsDesc(versions)

	for _, version := range versions {
		if cmd.Source == "all" {
			fmt.Fprintf(stdout, "%s\t%s\n", version, formatVersionSources(seen[version]))
			continue
		}
		fmt.Fprintln(stdout, version)
	}
	return nil
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

func runMCP(cmd mcpCmd, stdin io.Reader, stdout, stderr io.Writer) error {
	resolver, err := newResolver()
	if err != nil {
		return err
	}
	ts, err := toolsetfile.Load(cmd.Toolset)
	if err != nil {
		return err
	}

	prepared, err := ts.Prepare(context.Background(), resolver, toolset.Config{
		CredentialPolicySource: newCredentialPolicySource(),
	})
	if err != nil {
		return err
	}

	stdioServer := mcpgoserver.NewStdioServer(mcpserver.New(prepared))
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func runSDKBridgeServeStdio(stdin io.Reader, stdout, stderr io.Writer) error {
	_ = stderr
	resolver, err := newResolver()
	if err != nil {
		return err
	}

	bridge := sdkbridge.New(sdkbridge.Options{
		Resolver:               resolver,
		CredentialPolicySource: newCredentialPolicySource(),
	})
	return bridge.ServeStdio(context.Background(), stdin, stdout)
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
		if cache.Has(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version)) {
			pkg, err := cache.LoadArchive(registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
			if err != nil {
				return packageInfoView{}, err
			}
			return packageInfoView{
				Target:  target,
				Version: pkgVer.Version.String(),
				Source:  "cache",
				Package: pkg.Package,
			}, nil
		}
		result, err := resolver.Resolve(ctx, registry.ModulePath(pkgVer.Module), registry.Version(pkgVer.Version))
		if err != nil {
			return packageInfoView{}, err
		}
		return packageInfoView{
			Target:  target,
			Version: pkgVer.Version.String(),
			Source:  string(result.Metadata.ResolvedFrom),
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

func newCredentialPolicySource() toolset.PackageCredentialPolicySource {
	return credentialrepo.New(secrets.NewLocalSecretStore("", ""))
}

func newResolver() (*registry.Resolver, error) {
	cache, err := registry.NewCache("")
	if err != nil {
		return nil, err
	}
	githubBaseURL := os.Getenv("GITHUB_BASE_URL")
	gitURLPrefix := os.Getenv("TOOLBOX_GIT_URL_PREFIX")
	githubClient := newGitHubHTTPClient(os.Getenv("GITHUB_TOKEN"))

	sources := make([]registry.PackageSource, 0, 3)
	registryBaseURL, enabled, err := toolRegistryConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if enabled {
		source, err := registry.NewToolRegistrySource(registryBaseURL, newToolRegistryHTTPClient())
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	sources = append(
		sources,
		registry.NewGitHubReleaseSource(githubBaseURL, githubClient),
		&registry.GitSourceFallback{URLPrefix: gitURLPrefix},
	)

	return registry.NewResolver(cache, sources...), nil
}

func newGitHubHTTPClient(token string) *http.Client {
	if strings.TrimSpace(token) == "" {
		return http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: authTransport{base: transport, token: token}}
}

func newToolRegistryHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultToolRegistryTimeout}
}

func toolRegistryConfigFromEnv() (baseURL string, enabled bool, err error) {
	raw := strings.TrimSpace(os.Getenv("TOOLBOX_REGISTRY"))
	if raw == "" {
		raw = defaultToolRegistryBaseURL
	}
	if strings.EqualFold(raw, "off") {
		return "", false, nil
	}

	normalized, err := normalizeToolRegistryBaseURL(raw)
	if err != nil {
		return "", false, err
	}
	return normalized, true, nil
}

func normalizeToolRegistryBaseURL(raw string) (string, error) {
	original := raw
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: value must not be empty", original)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: %w", original, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: unsupported scheme %q", original, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: expected host or absolute URL", original)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header.Get("Authorization") == "" {
		clone.Header.Set("Authorization", "token "+t.token)
	}
	return t.base.RoundTrip(clone)
}
