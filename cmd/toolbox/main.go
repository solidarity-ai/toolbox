package main

import (
	"fmt"
	"io"
	"os"

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
	Package        packageCmd       `cmd:"" help:"Compile and pack Toolbox packages."`
	Version        versionCmd       `cmd:"" help:"Print the Toolbox binary version."`
	MCP            mcpCmd           `cmd:"" help:"Serve the selected toolset over MCP stdio."`
	Codemode       codemodeCmd      `cmd:"" help:"Codemode REPL and codemode MCP surfaces."`
	Auth           authCmd          `cmd:"" help:"Inspect and manage package authentication."`
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

type versionCmd struct {
	JSON bool `help:"Emit structured JSON output."`
}

type packageCmd struct {
	Pack packagePackCmd `cmd:"" help:"Compile a development package into distributable artifacts."`
}

type packagePackCmd struct {
	Directory string `arg:"" optional:"" default:"." name:"package-dir" type:"path" help:"Development package directory."`
	Out       string `name:"out" default:"." type:"path" help:"Output directory for the archive and manifest."`
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
	Status   authStatusCmd   `cmd:"" help:"Show auth state for one package or the active toolset."`
	List     authListCmd     `cmd:"" help:"List auth-relevant packages in the active toolset."`
	Accounts authAccountsCmd `cmd:"" help:"List accounts/profiles known for a package."`
	OAuth2   authOAuth2Cmd   `cmd:"" name:"oauth2" help:"Manage OAuth2 credentials."`
	Secret   authSecretCmd   `cmd:"" help:"Manage static secret credentials."`
	Setup    authSetupCmd    `cmd:"" help:"Initialize the secret store intentionally."`
	Unlock   authUnlockCmd   `cmd:"" help:"Unlock the secret store."`
	Lock     authLockCmd     `cmd:"" help:"Lock the secret store."`
	Recovery authRecoveryCmd `cmd:"" help:"Manage secret-store recovery codes."`
}

type authTargetFlags struct {
	Toolset    string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file used to resolve installed package targets."`
	Local      bool   `name:"local" help:"Treat TARGET as a local development package directory."`
	Credential string `name:"credential" help:"Credential name when a package declares more than one relevant credential."`
	Account    string `name:"account" help:"Account/profile name for account-scoped credentials."`
	JSON       bool   `name:"json" help:"Emit machine-readable JSON output."`
}

type authStatusCmd struct {
	authTargetFlags
	Target string `arg:"" optional:"" name:"target" help:"Package name/module target. Omit to show the active toolset summary."`
}

type authListCmd struct {
	Toolset string `name:"toolset" short:"t" default:"toolbox.toolset.json" type:"path" help:"Toolset file to inspect."`
	JSON    bool   `name:"json" help:"Emit machine-readable JSON output."`
}

type authAccountsCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authOAuth2Cmd struct {
	Status    authOAuth2StatusCmd    `cmd:"" help:"Show OAuth2-specific status."`
	Configure authOAuth2ConfigureCmd `cmd:"" help:"Configure OAuth2 client setup values."`
	Login     authOAuth2LoginCmd     `cmd:"" help:"Run OAuth2 authorization and store account token material."`
	Logout    authOAuth2LogoutCmd    `cmd:"" help:"Remove OAuth2 token material for an account."`
	Refresh   authOAuth2RefreshCmd   `cmd:"" help:"Refresh OAuth2 token material where supported."`
}

type authOAuth2StatusCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authOAuth2ConfigureCmd struct {
	authTargetFlags
	Stdin               bool   `name:"stdin" help:"Read client_id and client_secret from stdin, one line each. Blank client_secret is allowed for PKCE public clients."`
	ClientIDFromEnv     string `name:"client-id-from-env" help:"Read the OAuth2 client_id from this environment variable."`
	ClientSecretFromEnv string `name:"client-secret-from-env" help:"Read the OAuth2 client_secret from this environment variable."`
	Target              string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authOAuth2LoginCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authOAuth2LogoutCmd struct {
	authTargetFlags
	Yes    bool   `name:"yes" help:"Confirm destructive removal without prompting."`
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authOAuth2RefreshCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSecretCmd struct {
	Status   authSecretStatusCmd   `cmd:"" help:"Show static-secret-specific status."`
	Set      authSecretSetCmd      `cmd:"" help:"Set static secret material."`
	Clear    authSecretClearCmd    `cmd:"" help:"Clear static secret material."`
	Rotate   authSecretRotateCmd   `cmd:"" help:"Rotate static secret material."`
	Validate authSecretValidateCmd `cmd:"" help:"Validate static secret material where supported."`
}

type authSecretStatusCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSecretSetCmd struct {
	authTargetFlags
	Stdin           bool   `name:"stdin" help:"Read secret material from stdin instead of prompting."`
	FromEnv         string `name:"from-env" help:"Read single-value secret material from this environment variable."`
	UsernameFromEnv string `name:"username-from-env" help:"Read basic-auth username from this environment variable."`
	PasswordFromEnv string `name:"password-from-env" help:"Read basic-auth password from this environment variable."`
	Target          string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSecretClearCmd struct {
	authTargetFlags
	Yes    bool   `name:"yes" help:"Confirm destructive removal without prompting."`
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSecretRotateCmd struct {
	authTargetFlags
	Stdin           bool   `name:"stdin" help:"Read replacement secret material from stdin instead of prompting."`
	FromEnv         string `name:"from-env" help:"Read single-value replacement secret material from this environment variable."`
	UsernameFromEnv string `name:"username-from-env" help:"Read replacement basic-auth username from this environment variable."`
	PasswordFromEnv string `name:"password-from-env" help:"Read replacement basic-auth password from this environment variable."`
	Target          string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSecretValidateCmd struct {
	authTargetFlags
	Target string `arg:"" name:"target" help:"Package name/module target or local directory with --local."`
}

type authSetupCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON output."`
}

type authUnlockCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON output."`
}

type authLockCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON output."`
}

type authRecoveryCmd struct {
	Codes  authRecoveryCodesCmd  `cmd:"" help:"Generate replacement recovery codes."`
	Rewrap authRecoveryRewrapCmd `cmd:"" help:"Rewrap the secret store after unlocking with a recovery code."`
}

type authRecoveryCodesCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON output."`
}

type authRecoveryRewrapCmd struct {
	JSON bool `name:"json" help:"Emit machine-readable JSON output."`
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
		kong.ConfigureHelp(kong.HelpOptions{
			NoExpandSubcommands: true,
		}),
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
	if !securityCheckExempt(command) {
		if err := checkCurrentToolboxPolicy(stderr); err != nil {
			return err
		}
	}
	switch {
	case commandMatches(command, "version"):
		return runVersion(parsed.Version, stdout)
	case commandMatches(command, "install"):
		return runInstall(parsed.Install, stdout)
	case commandMatches(command, "update"):
		return runUpdate(parsed.Update, stdout)
	case commandMatches(command, "versions"):
		return runVersions(parsed.Versions, stdout)
	case commandMatches(command, "info"):
		return runInfo(parsed.Info, stdout)
	case commandMatches(command, "outdated"):
		return runOutdated(parsed.Outdated, stdout)
	case commandMatches(command, "search"):
		return runSearch(parsed.Search, stdout)
	case commandMatches(command, "package pack"):
		return runPackagePack(parsed.Package.Pack, stdout)
	case commandMatches(command, "mcp"):
		return runMCP(parsed.MCP, secretOpts, stdin, stdout, stderr)
	case commandMatches(command, "codemode repl"):
		return runRepl(parsed.Codemode.Repl, secretOpts, stdin, stdout, stderr)
	case commandMatches(command, "codemode session new"):
		return runCodemodeSessionNew(stdout)
	case commandMatches(command, "codemode mcp"):
		return runCodemodeMCP(parsed.Codemode.MCP, secretOpts, stdin, stdout, stderr)
	case commandMatches(command, "auth"):
		return runAuthCommand(parsed.Auth, command, secretOpts, stdin, stdout, stderr)
	case commandMatches(command, "daemon stop"):
		return runDaemonStop(stdout, stderr)
	case commandMatches(command, "daemon logs"):
		return runDaemonLogs(parsed.Daemon.Logs, stdout)
	case commandMatches(command, "_daemon serve"):
		return runDaemonServe(stderr)
	case commandMatches(command, "_daemon ping"):
		return runDaemonPing(parsed.InternalDaemon.Ping, stdout)
	case commandMatches(command, "_daemon stop"):
		return runDaemonStop(stdout, stderr)
	case commandMatches(command, "_sdkbridge serve-stdio"):
		return runSDKBridgeServeStdio(secretOpts, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}
