package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

type cli struct {
	NoDaemon       bool             `name:"no-daemon" help:"Use the local secret store directly instead of the daemon."`
	SecretKey      string           `name:"secret-key" env:"TOOLBOX_SECRET_KEY" help:"Unlock the secret store with this passphrase."`
	Install        installCmd       `cmd:"" help:"Resolve the selected toolset and write or update its lockfile."`
	Update         updateCmd        `cmd:"" help:"Update one installed package or all installed packages in the selected toolset."`
	Versions       versionsCmd      `cmd:"" help:"List cached and published versions for a package target."`
	Info           infoCmd          `cmd:"" help:"Show package manifest information for an installed target, local dir, or explicit package version."`
	Outdated       outdatedCmd      `cmd:"" help:"Show installed packages in the selected toolset with newer published versions."`
	Search         searchCmd        `cmd:"" help:"Search the tool registry for packages or tools."`
	MCP            mcpCmd           `cmd:"" help:"Serve the selected toolset over MCP stdio."`
	Codemode       codemodeCmd      `cmd:"" help:"Codemode REPL and codemode MCP surfaces."`
	Auth           authCmd          `cmd:"" help:"Legacy auth surface. This command is intentionally left on the existing parser while the auth CLI redesign is finalized."`
	Daemon         daemonControlCmd `cmd:"" name:"daemon" help:"Manage the toolbox daemon."`
	InternalDaemon daemonCmd        `cmd:"" name:"_daemon" hidden:"" help:"Internal daemon commands."`
	SDKBridge      sdkBridgeCmd     `cmd:"" name:"_sdkbridge" hidden:"" help:"Internal SDK bridge commands."`
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

type replCmd struct {
	Toolset   string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to load for session instructions."`
	Effects   string `help:"Comma-separated effects to include: readonly,reversible,irreversible."`
	TBSession string `name:"tb-session" help:"Existing tb_session to reopen. When omitted, repl creates and locks a fresh session."`
}

type mcpCmd struct {
	Toolset   string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to serve."`
	Effects   string `help:"Comma-separated effects to include: readonly,reversible,irreversible."`
	TBSession string `name:"tb-session" help:"Existing tb_session to lock to. When omitted, codemode mcp runs unlocked and requires tb_session in tool calls."`
}

type codemodeCmd struct {
	Session codemodeSessionCmd `cmd:"" help:"Create trusted codemode sessions."`
	Repl    replCmd            `cmd:"" help:"Start a persistent TypeScript REPL backed by tb_session storage."`
	MCP     mcpCmd             `cmd:"" name:"mcp" help:"Serve the selected toolset over the codemode MCP stdio surface."`
}

type codemodeSessionCmd struct {
	New codemodeSessionNewCmd `cmd:"" help:"Create a fresh tb_session."`
}

type codemodeSessionNewCmd struct{}

type authCmd struct {
	Args []string `arg:"" optional:"" passthrough:"all" name:"arg" help:"Legacy auth arguments."`
}

type daemonControlCmd struct {
	Stop daemonStopCmd `cmd:"" help:"Stop all running toolbox daemon processes."`
	Logs daemonLogsCmd `cmd:"" help:"Print daemon or MCP debug logs."`
}

type sdkBridgeCmd struct {
	ServeStdio sdkBridgeServeStdioCmd `cmd:"" name:"serve-stdio" hidden:"" help:"Serve the internal SDK bridge over stdio."`
}

type sdkBridgeServeStdioCmd struct{}

func (c cli) secretStoreOptions() secretStoreOptions {
	return secretStoreOptions{
		NoDaemon:  c.NoDaemon,
		SecretKey: c.SecretKey,
	}
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
	secretOpts := parsed.secretStoreOptions()
	secretOpts.BackupCodeWriter = stderr
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
	case strings.HasPrefix(command, "search"):
		return runSearch(parsed.Search, stdout)
	case strings.HasPrefix(command, "mcp"):
		return runMCP(parsed.MCP, secretOpts, stdin, stdout, stderr)
	case strings.HasPrefix(command, "codemode repl"):
		return runRepl(parsed.Codemode.Repl, secretOpts, stdin, stdout, stderr)
	case strings.HasPrefix(command, "codemode session new"):
		return runCodemodeSessionNew(stdout)
	case strings.HasPrefix(command, "codemode mcp"):
		return runCodemodeMCP(parsed.Codemode.MCP, secretOpts, stdin, stdout, stderr)
	case strings.HasPrefix(command, "auth"):
		return runAuth(parsed.Auth.Args, secretOpts, stdin, stdout, stderr)
	case strings.HasPrefix(command, "daemon stop"):
		return runDaemonStop(stdout, stderr)
	case strings.HasPrefix(command, "daemon logs"):
		return runDaemonLogs(parsed.Daemon.Logs, stdout)
	case strings.HasPrefix(command, "_daemon serve"):
		return runDaemonServe(stderr)
	case strings.HasPrefix(command, "_daemon ping"):
		return runDaemonPing(parsed.InternalDaemon.Ping, stdout)
	case strings.HasPrefix(command, "_daemon stop"):
		return runDaemonStop(stdout, stderr)
	case strings.HasPrefix(command, "_sdkbridge serve-stdio"):
		return runSDKBridgeServeStdio(secretOpts, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
