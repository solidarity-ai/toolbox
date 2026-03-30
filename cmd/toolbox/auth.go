package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const defaultBrowserLaunchTimeout = 10 * time.Second

type authPromptFunc func(in io.Reader, out io.Writer, prompt string, required bool) (string, error)
type authStoreFactory func() (secrets.SecretStore, error)
type authBootstrapFunc func(context.Context, oauthbootstrap.Request) (oauthbootstrap.Result, error)
type authBootstrapFactory func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc

type authDeps struct {
	prompt       authPromptFunc
	newStore     authStoreFactory
	newBootstrap authBootstrapFactory
	openBrowser  oauthbootstrap.BrowserOpener
}

var defaultAuthDeps = authDeps{
	prompt:      promptSecret,
	newStore:    newAuthLocalSecretStore,
	openBrowser: openBrowser,
	newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
		bootstrapper := oauthbootstrap.New(oauthbootstrap.Options{Store: store, OpenBrowser: opener})
		return bootstrapper.Run
	},
}

func runAuth(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runAuthWithDeps(args, stdin, stdout, stderr, defaultAuthDeps)
}

func runAuthWithDeps(args []string, stdin io.Reader, stdout, stderr io.Writer, deps authDeps) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tenant := fs.String("tenant", "", "tenant secret namespace")
	if err := fs.Parse(args); err != nil {
		printAuthUsage(stderr)
		return fmt.Errorf("auth: %w", err)
	}
	if fs.NArg() > 1 {
		printAuthUsage(stderr)
		return fmt.Errorf("auth: unexpected args: %s", strings.Join(fs.Args(), " "))
	}
	if deps.prompt == nil {
		deps.prompt = promptSecret
	}
	if deps.newStore == nil {
		deps.newStore = newAuthLocalSecretStore
	}
	if deps.openBrowser == nil {
		deps.openBrowser = openBrowser
	}
	if deps.newBootstrap == nil {
		deps.newBootstrap = defaultAuthDeps.newBootstrap
	}

	packagePath := "."
	if fs.NArg() == 1 {
		packagePath = fs.Arg(0)
	}

	loaded, err := packaging.LoadDev(packagePath)
	if err != nil {
		return fmt.Errorf("auth: package load failed for path %q: %w", packagePath, err)
	}
	credentialName, err := selectAuthCredential(loaded)
	if err != nil {
		return fmt.Errorf("auth: credential selection failed for path %q: %w", packagePath, err)
	}
	if strings.TrimSpace(*tenant) != "" {
		if _, err := tooldef.TenantSecretNamespace(loaded.Package.Module, *tenant); err != nil {
			return fmt.Errorf("auth: invalid --tenant %q for path %q: %w", *tenant, packagePath, err)
		}
	}

	bufferedInput := bufio.NewReader(stdin)

	clientID, err := deps.prompt(bufferedInput, stderr, "OAuth client_id", true)
	if err != nil {
		return fmt.Errorf("auth: prompt collection failed for path %q client_id: %w", packagePath, err)
	}
	clientSecret, err := deps.prompt(bufferedInput, stderr, "OAuth client_secret (optional; leave blank for PKCE public-client mode)", false)
	if err != nil {
		return fmt.Errorf("auth: prompt collection failed for path %q client_secret: %w", packagePath, err)
	}

	store, err := deps.newStore()
	if err != nil {
		return fmt.Errorf("auth: local secret store initialization failed for path %q: %w", packagePath, err)
	}
	bootstrap := deps.newBootstrap(store, deps.openBrowser)
	result, err := bootstrap(context.Background(), oauthbootstrap.Request{
		Package:        loaded,
		CredentialName: credentialName,
		Tenant:         strings.TrimSpace(*tenant),
		ClientID:       clientID,
		ClientSecret:   clientSecret,
	})
	if err != nil {
		return redactAuthError(packagePath, credentialName, strings.TrimSpace(*tenant), err)
	}

	scopeLabel := "package"
	if strings.TrimSpace(*tenant) != "" {
		scopeLabel = fmt.Sprintf("tenant %q", strings.TrimSpace(*tenant))
	}
	flowLabel := "confidential-client"
	if result.UsedPKCE {
		flowLabel = "public-client PKCE"
	}
	fmt.Fprintf(stdout, "authorized oauth2 credential %q for path %q (module %q, %s scope); stored %d durable secrets in %q via %s flow\n",
		result.CredentialName,
		packagePath,
		loaded.Package.Module.String(),
		scopeLabel,
		len(result.PersistedKeys),
		result.SecretNamespace,
		flowLabel,
	)
	return nil
}

func selectAuthCredential(pkg packaging.LoadedPackage) (string, error) {
	names := make([]string, 0, len(pkg.Package.Credentials))
	for _, credential := range pkg.Package.Credentials {
		if credential.Type == tooldef.CredentialTypeOAuth2 {
			names = append(names, credential.Name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("package %q declares no oauth2 credentials", pkg.Package.Name)
	}
	if len(names) > 1 {
		sort.Strings(names)
		return "", fmt.Errorf("package %q declares multiple oauth2 credentials; choose one of %s", pkg.Package.Name, strings.Join(names, ", "))
	}
	if strings.TrimSpace(names[0]) == "" {
		return "", fmt.Errorf("oauth2 credential name is required")
	}
	return names[0], nil
}

func promptSecret(in io.Reader, out io.Writer, prompt string, required bool) (string, error) {
	if in == nil {
		return "", fmt.Errorf("stdin is required")
	}
	if out == nil {
		out = io.Discard
	}
	if _, err := fmt.Fprintf(out, "%s: ", prompt); err != nil {
		return "", err
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if required && value == "" {
		return "", fmt.Errorf("value is required")
	}
	return value, nil
}

func newAuthLocalSecretStore() (secrets.SecretStore, error) {
	return secrets.NewLocalSecretStore("", ""), nil
}

func openBrowser(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid authorization url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid authorization url %q", rawURL)
	}
	launchCtx, cancel := context.WithTimeout(ctx, defaultBrowserLaunchTimeout)
	defer cancel()

	command, args, err := browserCommand(parsed.String())
	if err != nil {
		return err
	}
	output, err := exec.CommandContext(launchCtx, command, args...).CombinedOutput()
	if launchCtx.Err() != nil {
		return fmt.Errorf("browser launch timeout: %w", launchCtx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("%s: %w", detail, err)
		}
		return err
	}
	return nil
}

func browserCommand(rawURL string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{rawURL}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}, nil
	default:
		return "xdg-open", []string{rawURL}, nil
	}
}

func redactAuthError(packagePath, credentialName, tenant string, err error) error {
	contextLabel := fmt.Sprintf("path %q credential %q", packagePath, credentialName)
	if tenant != "" {
		contextLabel += fmt.Sprintf(" tenant %q", tenant)
	}
	var stageErr *oauthbootstrap.StageError
	if errors.As(err, &stageErr) {
		return fmt.Errorf("auth: %s failed for %s: %v", stageErr.Stage, contextLabel, stageErr.Err)
	}
	return fmt.Errorf("auth: bootstrap failed for %s: %v", contextLabel, err)
}

func printAuthUsage(f io.Writer) {
	fmt.Fprintln(f, "usage:")
	fmt.Fprintln(f, "  toolbox auth [--tenant TENANT] [PACKAGE_DIR]")
}
