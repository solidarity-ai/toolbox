package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/sdkbridge"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

const defaultToolsetFilename = "toolbox.toolset.json"

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
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("missing subcommand")
	}

	switch args[0] {
	case "resolve":
		return runResolve(args[1:], stdout)
	case "versions":
		return runVersions(args[1:], stdout)
	case "mcp":
		return runMCP(args[1:], stdin, stdout, stderr)
	case "auth":
		return runAuth(args[1:], stdin, stdout, stderr)
	case "_sdkbridge":
		return runSDKBridge(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runResolve(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(stdout)
	file := fs.String("file", defaultToolsetFilename, "toolset file")
	fileShort := fs.String("f", "", "toolset file")
	upgrade := fs.String("upgrade", "", "module path to upgrade before resolve")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fileShort != "" {
		file = fileShort
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("resolve: unexpected args: %s", strings.Join(fs.Args(), " "))
	}

	resolver, err := newResolver()
	if err != nil {
		return err
	}
	ctx := context.Background()

	ts, err := toolsetfile.Load(*file)
	if err != nil {
		return err
	}

	if strings.TrimSpace(*upgrade) != "" {
		module, err := tooldef.ParseModulePath(*upgrade)
		if err != nil {
			return fmt.Errorf("parse upgrade module path: %w", err)
		}
		currentRaw, ok := ts.Packages[module.String()]
		if !ok {
			return fmt.Errorf("upgrade module %q is not declared in %s", module, ts.SourceFilename())
		}
		current, err := tooldef.ParseVersion(currentRaw)
		if err != nil {
			return fmt.Errorf("parse current version for %s: %w", module, err)
		}
		versions, err := resolver.ListVersions(ctx, module)
		if err != nil {
			return err
		}
		latest := versions[0]
		if latest != current {
			if err := ts.SetPackageVersion(module, latest); err != nil {
				return err
			}
			if err := ts.Write(ts.SourceFilename()); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "upgraded %s: %s -> %s\n", module, current, latest)
		} else {
			fmt.Fprintf(stdout, "upgrade %s: already at %s\n", module, current)
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

func runVersions(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("versions", flag.ContinueOnError)
	fs.SetOutput(stdout)
	file := fs.String("file", defaultToolsetFilename, "toolset file (optional context only)")
	fileShort := fs.String("f", "", "toolset file (optional context only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fileShort != "" {
		file = fileShort
	}
	_ = file
	if fs.NArg() != 1 {
		return fmt.Errorf("versions: expected exactly one module path argument")
	}
	module, err := tooldef.ParseModulePath(fs.Arg(0))
	if err != nil {
		return err
	}

	resolver, err := newResolver()
	if err != nil {
		return err
	}
	versions, err := resolver.ListVersions(context.Background(), module)
	if err != nil {
		return err
	}
	for _, version := range versions {
		fmt.Fprintln(stdout, version.String())
	}
	return nil
}

func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("mcp: missing subcommand")
	}

	switch args[0] {
	case "serve":
		return runMCPServe(args[1:], stdin, stdout, stderr)
	default:
		printUsage(stderr)
		return fmt.Errorf("mcp: unknown subcommand %q", args[0])
	}
}

func runMCPServe(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", defaultToolsetFilename, "toolset file")
	fileShort := fs.String("f", "", "toolset file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fileShort != "" {
		file = fileShort
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("mcp serve: unexpected args: %s", strings.Join(fs.Args(), " "))
	}

	resolver, err := newResolver()
	if err != nil {
		return err
	}
	ts, err := toolsetfile.Load(*file)
	if err != nil {
		return err
	}

	ctx := context.Background()

	cfg := toolset.Config{
		CredentialPolicySource: newCredentialPolicySource(),
	}

	prepared, err := ts.Prepare(ctx, resolver, cfg)
	if err != nil {
		return err
	}

	stdioServer := mcpgoserver.NewStdioServer(mcpserver.New(prepared))
	stdioServer.SetErrorLogger(log.New(stderr, "", log.LstdFlags))
	return stdioServer.Listen(context.Background(), stdin, stdout)
}

func runSDKBridge(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("_sdkbridge: missing subcommand")
	}

	switch args[0] {
	case "serve-stdio":
		return runSDKBridgeServeStdio(args[1:], stdin, stdout, stderr)
	default:
		return fmt.Errorf("_sdkbridge: unknown subcommand %q", args[0])
	}
}

func runSDKBridgeServeStdio(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("_sdkbridge serve-stdio", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("_sdkbridge serve-stdio: unexpected args: %s", strings.Join(fs.Args(), " "))
	}

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
	client := newGitHubHTTPClient(os.Getenv("GITHUB_TOKEN"))
	return registry.NewResolver(
		cache,
		registry.NewGitHubReleaseSource(githubBaseURL, client),
		&registry.GitSourceFallback{URLPrefix: gitURLPrefix},
	), nil
}

func newGitHubHTTPClient(token string) *http.Client {
	if strings.TrimSpace(token) == "" {
		return http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: authTransport{base: transport, token: token}}
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

func printUsage(f io.Writer) {
	fmt.Fprintln(f, "usage:")
	fmt.Fprintln(f, "  toolbox resolve [--file FILE] [--upgrade MODULE]")
	fmt.Fprintln(f, "  toolbox versions [--file FILE] <module>")
	fmt.Fprintln(f, "  toolbox mcp serve [--file FILE]")
	fmt.Fprintln(f, "  toolbox auth [--check|--delete-account NAME|--delete-credential --credential NAME] <package-dir>")
}
