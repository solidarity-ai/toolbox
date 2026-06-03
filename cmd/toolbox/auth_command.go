package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolsetfile"
	"github.com/solidarity-ai/toolbox/transport"
)

type authCommandResult struct {
	State   string   `json:"state"`
	Message string   `json:"message,omitempty"`
	Codes   []string `json:"codes,omitempty"`
}

type authPackageStatus struct {
	Package     string                 `json:"package"`
	Module      string                 `json:"module"`
	TargetClass string                 `json:"target_class"`
	Source      string                 `json:"source,omitempty"`
	Required    bool                   `json:"required"`
	State       string                 `json:"state"`
	Credentials []authCredentialStatus `json:"credentials"`
	NextSteps   []string               `json:"next_steps,omitempty"`
}

type authCredentialStatus struct {
	Name      string              `json:"name"`
	Type      string              `json:"type"`
	State     string              `json:"state"`
	Shared    []authSecretStatus  `json:"shared,omitempty"`
	Accounts  []authAccountStatus `json:"accounts,omitempty"`
	NextSteps []string            `json:"next_steps,omitempty"`
}

type authAccountStatus struct {
	Account string             `json:"account"`
	State   string             `json:"state"`
	Secrets []authSecretStatus `json:"secrets,omitempty"`
}

type authSecretStatus struct {
	Label    string `json:"label"`
	State    string `json:"state"`
	Required bool   `json:"required"`
}

type resolvedAuthTarget struct {
	Loaded      packaging.LoadedPackage
	TargetClass string
	Context     string
	Source      string
}

func runAuthCommand(cmd authCmd, command string, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	// New auth commands require intentional `toolbox auth setup`; do not allow
	// ordinary credential reads/writes to auto-initialize and print recovery
	// codes as a side effect of --secret-key auto-unlock.
	opts.BackupCodeWriter = nil
	switch command {
	case "auth":
		return fmt.Errorf("auth: expected subcommand (status, list, accounts, oauth2, secret, setup, unlock, lock, recovery)")
	case "auth status <target>", "auth status":
		if cmd.Status.Target == "" {
			return runAuthList(cmd.AuthList(), opts, stdout)
		}
		return runAuthStatus(cmd.Status.authTargetFlags, cmd.Status.Target, "", opts, stdout)
	case "auth list":
		return runAuthList(cmd.List, opts, stdout)
	case "auth accounts <target>":
		return runAuthAccounts(cmd.Accounts.authTargetFlags, cmd.Accounts.Target, opts, stdout)
	case "auth oauth2 status <target>":
		return runAuthStatus(cmd.OAuth2.Status.authTargetFlags, cmd.OAuth2.Status.Target, "oauth2", opts, stdout)
	case "auth oauth2 configure <target>":
		return runAuthOAuth2Configure(cmd.OAuth2.Configure, opts, stdin, stdout)
	case "auth oauth2 login <target>":
		return runAuthOAuth2Login(cmd.OAuth2.Login, opts, stdin, stdout, stderr)
	case "auth oauth2 logout <target>":
		return runAuthOAuth2Logout(cmd.OAuth2.Logout, opts, stdin, stdout)
	case "auth oauth2 refresh <target>":
		return stableAuthError("unsupported_auth_type", "OAuth2 refresh is not supported by the stored credential format; run `toolbox auth oauth2 login` again if the token is invalid")
	case "auth secret status <target>":
		return runAuthStatus(cmd.Secret.Status.authTargetFlags, cmd.Secret.Status.Target, "secret", opts, stdout)
	case "auth secret set <target>":
		return runAuthSecretSet(cmd.Secret.Set, opts, stdin, stdout, stderr)
	case "auth secret rotate <target>":
		set := authSecretSetCmd(cmd.Secret.Rotate)
		return runAuthSecretSet(set, opts, stdin, stdout, stderr)
	case "auth secret clear <target>":
		return runAuthSecretClear(cmd.Secret.Clear, opts, stdin, stdout)
	case "auth secret validate <target>":
		return stableAuthError("unsupported_auth_type", "static secret validation is not supported for this package; configured secrets can still be inspected with `toolbox auth secret status`")
	case "auth setup":
		return runAuthSetup(cmd.Setup.JSON, opts, stdin, stdout)
	case "auth unlock":
		return runAuthUnlock(cmd.Unlock.JSON, opts, stdin, stdout)
	case "auth lock":
		return runAuthLock(cmd.Lock.JSON, opts, stdout)
	case "auth recovery codes":
		return runAuthRecoveryCodes(cmd.Recovery.Codes.JSON, opts, stdin, stdout)
	case "auth recovery rewrap":
		return runAuthRecoveryRewrap(cmd.Recovery.Rewrap.JSON, opts, stdin, stdout)
	default:
		return fmt.Errorf("auth: unsupported command %q", command)
	}
}

func (cmd authCmd) AuthList() authListCmd {
	return authListCmd{Toolset: cmd.Status.Toolset, JSON: cmd.Status.JSON}
}

func runAuthStatus(flags authTargetFlags, target, only string, opts secretStoreOptions, stdout io.Writer) error {
	resolved, err := resolveAuthTarget(context.Background(), flags, target)
	if err != nil {
		return authScopeError(err)
	}
	status, err := packageAuthStatus(context.Background(), resolved, newCredentialRepository(opts), only, flags.Credential, flags.Account)
	if err != nil {
		return classifySecretStoreError(err)
	}
	if flags.JSON {
		return writeJSON(stdout, status)
	}
	writePackageAuthStatus(stdout, status)
	return nil
}

func runAuthList(cmd authListCmd, opts secretStoreOptions, stdout io.Writer) error {
	ctx := context.Background()
	resolver, err := newResolver()
	if err != nil {
		return err
	}
	ts, err := toolsetfile.Load(cmd.Toolset)
	if err != nil {
		return err
	}
	loaded, err := loadInstalledPackages(ctx, resolver, ts)
	if err != nil {
		return err
	}
	local, err := ts.LoadLocal()
	if err != nil {
		return err
	}
	repo := newCredentialRepository(opts)
	statuses := make([]authPackageStatus, 0, len(loaded.Packages.Packages))
	for _, pkg := range loaded.Packages.Packages {
		class := installedContext(pkg.Local)
		source := cmd.Toolset
		if class == "override" {
			if dir, ok := local.ReplacementDirAbs(pkg.Package.Package.Module); ok {
				source = dir
			}
		}
		status, err := packageAuthStatus(ctx, resolvedAuthTarget{Loaded: pkg.Package, TargetClass: class, Context: packageContextLabel(pkg.Package.Package, class), Source: source}, repo, "", "", "")
		if err != nil {
			return classifySecretStoreError(err)
		}
		statuses = append(statuses, status)
	}
	if cmd.JSON {
		return writeJSON(stdout, statuses)
	}
	fmt.Fprintln(stdout, "PACKAGE      REQUIRED  STATUS                 NEXT STEP")
	for _, status := range statuses {
		next := "-"
		if len(status.NextSteps) > 0 {
			next = status.NextSteps[0]
		}
		fmt.Fprintf(stdout, "%-12s %-9s %-22s %s\n", status.Package, yesNo(status.Required), status.State, next)
	}
	return nil
}

func runAuthAccounts(flags authTargetFlags, target string, opts secretStoreOptions, stdout io.Writer) error {
	resolved, err := resolveAuthTarget(context.Background(), flags, target)
	if err != nil {
		return authScopeError(err)
	}
	accounts, err := newCredentialRepository(opts).DiscoverCredentialAccounts(context.Background(), resolved.Loaded.Package)
	if err != nil {
		return classifySecretStoreError(err)
	}
	accounts = filterDiscoveredAccounts(accounts, flags.Credential)
	if flags.JSON {
		return writeJSON(stdout, accounts)
	}
	fmt.Fprintf(stdout, "Package: %s\nContext: %s\n", resolved.Loaded.Package.Name, resolved.Context)
	for _, cred := range resolved.Loaded.Package.Credentials {
		if flags.Credential != "" && cred.Name != flags.Credential {
			continue
		}
		fmt.Fprintf(stdout, "\nCredential: %s\nType: %s\n", cred.Name, cred.Type)
		list := accounts[cred.Name]
		if len(list) == 0 {
			fmt.Fprintln(stdout, "Accounts: none")
			continue
		}
		fmt.Fprintln(stdout, "Accounts:")
		for _, acct := range list {
			fmt.Fprintf(stdout, "  - %s\n", acct)
		}
	}
	return nil
}

func runAuthOAuth2Configure(cmd authOAuth2ConfigureCmd, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	resolved, cred, err := selectCredentialForCommand(cmd.authTargetFlags, cmd.Target, "oauth2", opts, false)
	if err != nil {
		return err
	}
	ctx := context.Background()
	repo := newCredentialRepository(opts)
	clientID, clientSecret, err := readOAuth2ClientConfig(cmd, stdin, stdout, cred)
	if err != nil {
		return err
	}
	if err := repo.Set(ctx, credentialrepo.OAuth2ClientIDRef(resolved.Loaded.Package, cred.Name), []byte(clientID)); err != nil {
		return classifySecretStoreError(err)
	}
	secretRef := credentialrepo.OAuth2ClientSecretRef(resolved.Loaded.Package, cred.Name)
	if clientSecret == "" {
		_ = repo.Delete(ctx, secretRef)
	} else if err := repo.Set(ctx, secretRef, []byte(clientSecret)); err != nil {
		return classifySecretStoreError(err)
	}
	fmt.Fprintf(stdout, "Configuring OAuth2 credential in %s\n", resolved.Context)
	fmt.Fprintf(stdout, "Configured OAuth2 client values for %s. Secret values were not printed.\n", cred.Name)
	return nil
}

func runAuthOAuth2Login(cmd authOAuth2LoginCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	resolved, cred, err := selectCredentialForCommand(cmd.authTargetFlags, cmd.Target, "oauth2", opts, false)
	if err != nil {
		return err
	}
	ctx := context.Background()
	repo := newCredentialRepository(opts)
	account, err := selectAccountForMutation(ctx, repo, resolved.Loaded.Package, cred, cmd.Account)
	if err != nil {
		return err
	}
	if !readerIsInteractiveTTY(stdin) {
		if err := requireOAuth2ClientConfig(ctx, repo, resolved.Loaded.Package, cred); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "Authenticating OAuth2 credential in %s\n", resolved.Context)
	if err := authOAuth2(ctx, repo, newAuthInput(stdin), resolved.Loaded.Package, cred, account, stdout, stderr, authRunOptions{PreferDaemonOAuth: !opts.NoDaemon}); err != nil {
		return classifySecretStoreError(err)
	}
	return nil
}

func runAuthOAuth2Logout(cmd authOAuth2LogoutCmd, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	resolved, cred, err := selectCredentialForCommand(cmd.authTargetFlags, cmd.Target, "oauth2", opts, true)
	if err != nil {
		return err
	}
	account, err := selectAccountForMutation(context.Background(), newCredentialRepository(opts), resolved.Loaded.Package, cred, cmd.Account)
	if err != nil {
		return err
	}
	if !cmd.Yes && !confirm(stdin, stdout, fmt.Sprintf("Remove OAuth2 token for package %s, credential %s, account %s (secret labels: refresh_token)?", resolved.Loaded.Package.Name, cred.Name, account)) {
		fmt.Fprintln(stdout, "Cancelled.")
		return nil
	}
	ref := credentialrepo.OAuth2RefreshTokenRef(resolved.Loaded.Package, cred.Name, account)
	if err := newCredentialRepository(opts).Delete(context.Background(), ref); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return classifySecretStoreError(err)
	}
	fmt.Fprintf(stdout, "Removed OAuth2 token for %s / %s / %s. OAuth2 client configuration was left intact.\n", resolved.Loaded.Package.Name, cred.Name, account)
	return nil
}

func runAuthSecretSet(cmd authSecretSetCmd, opts secretStoreOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	resolved, cred, err := selectCredentialForCommand(cmd.authTargetFlags, cmd.Target, "secret", opts, false)
	if err != nil {
		return err
	}
	ctx := context.Background()
	repo := newCredentialRepository(opts)
	account, err := selectAccountForMutation(ctx, repo, resolved.Loaded.Package, cred, cmd.Account)
	if err != nil {
		return err
	}
	reader, err := secretInputReader(cmd, cred, stdin)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Configuring static secret in %s\n", resolved.Context)
	switch cred.Type {
	case "api_key":
		if err := authAPIKey(ctx, repo, newAuthInput(reader), resolved.Loaded.Package, cred, account, stdout); err != nil {
			return classifySecretStoreError(err)
		}
		return nil
	case "bearer":
		if err := authBearer(ctx, repo, newAuthInput(reader), resolved.Loaded.Package, cred, account, stdout); err != nil {
			return classifySecretStoreError(err)
		}
		return nil
	default:
		return stableAuthError("unsupported_auth_type", fmt.Sprintf("credential %s has unsupported static secret type %q", cred.Name, cred.Type))
	}
}

func runAuthSecretClear(cmd authSecretClearCmd, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	resolved, cred, err := selectCredentialForCommand(cmd.authTargetFlags, cmd.Target, "secret", opts, true)
	if err != nil {
		return err
	}
	repo := newCredentialRepository(opts)
	account, err := selectAccountForMutation(context.Background(), repo, resolved.Loaded.Package, cred, cmd.Account)
	if err != nil {
		return err
	}
	if !cmd.Yes && !confirm(stdin, stdout, fmt.Sprintf("Clear static secrets for package %s, credential %s, account %s (secret labels: %s)?", resolved.Loaded.Package.Name, cred.Name, account, strings.Join(staticSecretLabels(cred), ", "))) {
		fmt.Fprintln(stdout, "Cancelled.")
		return nil
	}
	results, err := repo.DeleteAccount(context.Background(), resolved.Loaded.Package, cred.Name, account)
	if err != nil {
		return classifySecretStoreError(err)
	}
	deleted := 0
	for _, result := range results {
		deleted += result.Deleted
	}
	fmt.Fprintf(stdout, "Cleared static secret material for %s / %s / %s (%d secrets removed).\n", resolved.Loaded.Package.Name, cred.Name, account, deleted)
	return nil
}

func runAuthSetup(jsonOut bool, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	store := newLifecycleSecretStore(opts)
	promptOut := stdout
	if jsonOut {
		promptOut = io.Discard
	}
	key, err := readPassphrase(stdin, promptOut, "Create secret-store passphrase: ")
	if err != nil {
		return err
	}
	codes, err := store.Setup(context.Background(), key)
	if err != nil {
		return classifySecretStoreError(err)
	}
	if jsonOut {
		return writeJSON(stdout, authCommandResult{State: "configured", Codes: codes})
	}
	fmt.Fprintln(stdout, "Secret store initialized. Save these recovery codes now; they will not be printed again automatically:")
	secrets.WriteBackupCodes(stdout, codes)
	return nil
}

func runAuthUnlock(jsonOut bool, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	promptOut := stdout
	if jsonOut {
		promptOut = io.Discard
	}
	key, err := readPassphrase(stdin, promptOut, "Secret-store passphrase or recovery code: ")
	if err != nil {
		return err
	}
	if err := newManagedSecretStore(opts).Unlock(context.Background(), key); err != nil {
		return classifySecretStoreError(err)
	}
	if jsonOut {
		return writeJSON(stdout, authCommandResult{State: "authenticated"})
	}
	fmt.Fprintln(stdout, "Secret store unlocked.")
	return nil
}

func runAuthLock(jsonOut bool, opts secretStoreOptions, stdout io.Writer) error {
	if err := newManagedSecretStore(opts).Lock(context.Background()); err != nil {
		return classifySecretStoreError(err)
	}
	if jsonOut {
		return writeJSON(stdout, authCommandResult{State: "secret_store_locked"})
	}
	fmt.Fprintln(stdout, "Secret store locked.")
	return nil
}

func runAuthRecoveryCodes(jsonOut bool, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	promptOut := stdout
	if jsonOut {
		promptOut = io.Discard
	}
	key, err := readLineWithPrompt(stdin, promptOut, "Secret-store passphrase (leave blank if already unlocked with a recovery code): ")
	if err != nil {
		return err
	}
	store := newLifecycleSecretStore(opts)
	var codes []string
	if strings.TrimSpace(key) == "" {
		codes, err = store.RecoveryCodes(context.Background())
	} else {
		codes, err = store.BackupCodes(context.Background(), key)
	}
	if err != nil {
		return classifySecretStoreError(err)
	}
	if jsonOut {
		return writeJSON(stdout, authCommandResult{State: "configured", Codes: codes})
	}
	fmt.Fprintln(stdout, "Recovery codes:")
	secrets.WriteBackupCodes(stdout, codes)
	return nil
}

func runAuthRecoveryRewrap(jsonOut bool, opts secretStoreOptions, stdin io.Reader, stdout io.Writer) error {
	promptOut := stdout
	if jsonOut {
		promptOut = io.Discard
	}
	key, err := readPassphrase(stdin, promptOut, "New secret-store passphrase: ")
	if err != nil {
		return err
	}
	if err := newLifecycleSecretStore(opts).RewrapAfterRecovery(context.Background(), key); err != nil {
		return classifySecretStoreError(err)
	}
	if jsonOut {
		return writeJSON(stdout, authCommandResult{State: "configured"})
	}
	fmt.Fprintln(stdout, "Secret store rewrapped with the new passphrase.")
	return nil
}

func resolveAuthTarget(ctx context.Context, flags authTargetFlags, target string) (resolvedAuthTarget, error) {
	if strings.TrimSpace(target) == "" {
		return resolvedAuthTarget{}, fmt.Errorf("ambiguous_target: target is required")
	}
	if flags.Local {
		loaded, err := packaging.LoadDev(target)
		if err != nil {
			return resolvedAuthTarget{}, fmt.Errorf("loading local package %s: %w", target, err)
		}
		return resolvedAuthTarget{Loaded: loaded, TargetClass: "local", Context: fmt.Sprintf("local package %s (%s)", loaded.Package.Name, target), Source: target}, nil
	}
	module, parseErr := tooldef.ParseModulePath(target)
	if strings.HasPrefix(target, ".") || parseErr != nil && strings.Contains(target, string(os.PathSeparator)) {
		return resolvedAuthTarget{}, fmt.Errorf("ambiguous_target: local package directories require --local to avoid writing development credentials by accident")
	}
	resolver, err := newResolver()
	if err != nil {
		return resolvedAuthTarget{}, err
	}
	ts, err := toolsetfile.Load(flags.Toolset)
	if err != nil {
		return resolvedAuthTarget{}, err
	}
	local, err := ts.LoadLocal()
	if err != nil {
		return resolvedAuthTarget{}, err
	}
	loaded, err := loadInstalledPackages(ctx, resolver, ts)
	if err != nil {
		return resolvedAuthTarget{}, err
	}
	for _, pkg := range loaded.Packages.Packages {
		manifest := pkg.Package.Package
		if (parseErr == nil && manifest.Module == module) || manifest.Name == target {
			class := installedContext(pkg.Local)
			source := flags.Toolset
			if class == "override" {
				if dir, ok := local.ReplacementDirAbs(manifest.Module); ok {
					source = dir
				}
			}
			return resolvedAuthTarget{Loaded: pkg.Package, TargetClass: class, Context: packageContextLabel(manifest, class), Source: source}, nil
		}
	}
	return resolvedAuthTarget{}, fmt.Errorf("ambiguous_target: package %q is not installed in %s", target, flags.Toolset)
}

func selectCredentialForCommand(flags authTargetFlags, target, workflow string, opts secretStoreOptions, requireConfigured bool) (resolvedAuthTarget, tooldef.PackageCredential, error) {
	resolved, err := resolveAuthTarget(context.Background(), flags, target)
	if err != nil {
		return resolvedAuthTarget{}, tooldef.PackageCredential{}, authScopeError(err)
	}
	matches := matchingCredentials(resolved.Loaded.Package.Credentials, flags.Credential, workflow)
	if flags.Credential != "" && len(matches) == 0 {
		return resolvedAuthTarget{}, tooldef.PackageCredential{}, stableAuthError("ambiguous_credential", fmt.Sprintf("credential %q was not found for workflow %s", flags.Credential, workflow))
	}
	if len(matches) == 0 {
		return resolvedAuthTarget{}, tooldef.PackageCredential{}, stableAuthError("unsupported_auth_type", fmt.Sprintf("package %s has no %s credentials", resolved.Loaded.Package.Name, workflow))
	}
	if len(matches) > 1 {
		return resolvedAuthTarget{}, tooldef.PackageCredential{}, ambiguousCredentialError(resolved.Loaded.Package.Name, workflow, matches)
	}
	if flags.Account != "" {
		if err := transport.ValidateAccountString(flags.Account); err != nil {
			return resolvedAuthTarget{}, tooldef.PackageCredential{}, err
		}
	}
	if requireConfigured {
		if _, err := selectAccountForMutation(context.Background(), newCredentialRepository(opts), resolved.Loaded.Package, matches[0], flags.Account); err != nil {
			return resolvedAuthTarget{}, tooldef.PackageCredential{}, err
		}
	}
	return resolved, matches[0], nil
}

func matchingCredentials(creds []tooldef.PackageCredential, name, workflow string) []tooldef.PackageCredential {
	var out []tooldef.PackageCredential
	for _, cred := range creds {
		if name != "" && cred.Name != name {
			continue
		}
		if workflow == "oauth2" && cred.Type != "oauth2" {
			continue
		}
		if workflow == "secret" && cred.Type == "oauth2" {
			continue
		}
		out = append(out, cred)
	}
	return out
}

func filterDiscoveredAccounts(accounts map[string][]string, credential string) map[string][]string {
	if credential == "" {
		return accounts
	}
	list, ok := accounts[credential]
	if !ok {
		return map[string][]string{}
	}
	return map[string][]string{credential: list}
}

func filterCredentialCheckAccount(check credentialrepo.CredentialCheck, account string) credentialrepo.CredentialCheck {
	if account == "" {
		return check
	}
	accounts := make([]credentialrepo.AccountCheck, 0, 1)
	for _, acct := range check.Accounts {
		if acct.Account == account {
			accounts = append(accounts, acct)
			break
		}
	}
	check.Accounts = accounts
	return check
}

func packageAuthStatus(ctx context.Context, target resolvedAuthTarget, repo *credentialrepo.Repository, only, credential, account string) (authPackageStatus, error) {
	pkg := target.Loaded.Package
	status := authPackageStatus{Package: pkg.Name, Module: pkg.Module.String(), TargetClass: target.TargetClass, Source: target.Source, Required: len(pkg.Credentials) > 0, State: "not_required"}
	if len(pkg.Credentials) == 0 {
		return status, nil
	}
	if account != "" {
		if err := transport.ValidateAccountString(account); err != nil {
			return status, err
		}
	}
	checks, err := repo.CheckPackage(ctx, pkg)
	if err != nil {
		return status, err
	}
	status.State = "authenticated"
	for _, check := range checks.Credentials {
		if credential != "" && check.Credential.Name != credential {
			continue
		}
		if only == "oauth2" && check.Credential.Type != "oauth2" || only == "secret" && check.Credential.Type == "oauth2" {
			continue
		}
		check = filterCredentialCheckAccount(check, account)
		credStatus := credentialAuthStatus(pkg.Name, check)
		status.Credentials = append(status.Credentials, credStatus)
		if credStatus.State != "authenticated" && credStatus.State != "configured" {
			status.State = credStatus.State
			status.NextSteps = append(status.NextSteps, credStatus.NextSteps...)
		}
	}
	if len(status.Credentials) == 0 {
		status.State = "unsupported_auth_type"
	}
	if status.State == "authenticated" && only == "secret" {
		status.State = "configured"
	}
	return status, nil
}

func credentialAuthStatus(pkgName string, check credentialrepo.CredentialCheck) authCredentialStatus {
	state := "authenticated"
	if check.Credential.Type != "oauth2" {
		state = "configured"
	}
	var next []string
	for _, shared := range check.Shared {
		s := secretState(shared)
		if s.State == "missing_credentials" {
			state = "missing_credentials"
		}
	}
	accounts := make([]authAccountStatus, 0, len(check.Accounts))
	if len(check.Accounts) == 0 {
		state = "missing_credentials"
	}
	for _, acct := range check.Accounts {
		acctState := state
		if acct.MissingAccount {
			acctState = "missing_credentials"
		}
		secretsOut := make([]authSecretStatus, 0, len(acct.Secrets))
		for _, secret := range acct.Secrets {
			s := secretState(secret)
			secretsOut = append(secretsOut, s)
			if s.State == "missing_credentials" {
				acctState = "missing_credentials"
			}
		}
		if acctState == "missing_credentials" {
			state = "missing_credentials"
		}
		accounts = append(accounts, authAccountStatus{Account: acct.Account, State: acctState, Secrets: secretsOut})
	}
	if state == "missing_credentials" {
		if check.Credential.Type == "oauth2" {
			next = append(next, fmt.Sprintf("toolbox auth oauth2 login %s --credential %s", pkgName, check.Credential.Name))
		} else {
			next = append(next, fmt.Sprintf("toolbox auth secret set %s --credential %s", pkgName, check.Credential.Name))
		}
	}
	shared := make([]authSecretStatus, 0, len(check.Shared))
	for _, secret := range check.Shared {
		shared = append(shared, secretState(secret))
	}
	return authCredentialStatus{Name: check.Credential.Name, Type: check.Credential.Type, State: state, Shared: shared, Accounts: accounts, NextSteps: next}
}

func secretState(check credentialrepo.SecretCheck) authSecretStatus {
	state := "configured"
	if check.Required && !check.Present {
		state = "missing_credentials"
	} else if !check.Required && !check.Present {
		state = "not_required"
	}
	return authSecretStatus{Label: check.Label, State: state, Required: check.Required}
}

func writePackageAuthStatus(stdout io.Writer, status authPackageStatus) {
	fmt.Fprintf(stdout, "Package: %s\nModule: %s\nContext: %s\n", status.Package, status.Module, status.TargetClass)
	if status.Source != "" {
		fmt.Fprintf(stdout, "Source: %s\n", status.Source)
	}
	fmt.Fprintf(stdout, "Status: %s\n", status.State)
	if !status.Required {
		fmt.Fprintln(stdout, "Authentication is not required.")
		return
	}
	for _, cred := range status.Credentials {
		fmt.Fprintf(stdout, "\nCredential: %s\nType: %s\nStatus: %s\n", cred.Name, cred.Type, cred.State)
		for _, shared := range cred.Shared {
			fmt.Fprintf(stdout, "  %s: %s\n", shared.Label, shared.State)
		}
		for _, acct := range cred.Accounts {
			fmt.Fprintf(stdout, "  Account: %s (%s)\n", acct.Account, acct.State)
			for _, secret := range acct.Secrets {
				fmt.Fprintf(stdout, "    %s: %s\n", secret.Label, secret.State)
			}
		}
		for _, next := range cred.NextSteps {
			fmt.Fprintf(stdout, "Next step:\n  %s\n", next)
		}
	}
}

func readOAuth2ClientConfig(cmd authOAuth2ConfigureCmd, stdin io.Reader, stdout io.Writer, cred tooldef.PackageCredential) (string, string, error) {
	if cmd.ClientIDFromEnv != "" {
		id := os.Getenv(cmd.ClientIDFromEnv)
		if id == "" {
			return "", "", fmt.Errorf("auth: environment variable %s is empty", cmd.ClientIDFromEnv)
		}
		secret := ""
		if cmd.ClientSecretFromEnv != "" {
			secret = os.Getenv(cmd.ClientSecretFromEnv)
		}
		return id, secret, nil
	}
	if cmd.Stdin {
		scanner := bufio.NewScanner(stdin)
		if !scanner.Scan() {
			return "", "", fmt.Errorf("auth: missing client_id on stdin")
		}
		id := scanner.Text()
		secret := ""
		if scanner.Scan() {
			secret = scanner.Text()
		}
		if id == "" {
			return "", "", fmt.Errorf("auth: client_id cannot be empty")
		}
		return id, secret, scanner.Err()
	}
	input := newAuthInput(stdin)
	fmt.Fprintf(stdout, "Enter client_id for %s: ", cred.Name)
	id, err := input.ReadLine(context.Background())
	if err != nil {
		return "", "", err
	}
	if id == "" {
		return "", "", fmt.Errorf("auth: client_id cannot be empty")
	}
	fmt.Fprintf(stdout, "Enter client_secret for %s (press Enter to skip for PKCE public clients): ", cred.Name)
	secret, err := input.ReadLine(context.Background())
	return id, secret, err
}

func requireOAuth2ClientConfig(ctx context.Context, repo *credentialrepo.Repository, pkg tooldef.Package, cred tooldef.PackageCredential) error {
	if _, err := getRequiredSecret(ctx, repo, credentialrepo.OAuth2ClientIDRef(pkg, cred.Name)); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return stableAuthError("missing_credentials", fmt.Sprintf("OAuth2 client configuration for %s is missing; run `toolbox auth oauth2 configure %s --credential %s` first", cred.Name, pkg.Name, cred.Name))
		}
		return classifySecretStoreError(err)
	}
	if cred.Provider == nil || !cred.Provider.PKCEEnabled() {
		if _, err := getRequiredSecret(ctx, repo, credentialrepo.OAuth2ClientSecretRef(pkg, cred.Name)); err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return stableAuthError("missing_credentials", fmt.Sprintf("OAuth2 client_secret for %s is missing; run `toolbox auth oauth2 configure %s --credential %s` first", cred.Name, pkg.Name, cred.Name))
			}
			return classifySecretStoreError(err)
		}
	}
	return nil
}

func secretInputReader(cmd authSecretSetCmd, cred tooldef.PackageCredential, stdin io.Reader) (io.Reader, error) {
	if cmd.FromEnv != "" {
		value := os.Getenv(cmd.FromEnv)
		if value == "" {
			return nil, fmt.Errorf("auth: environment variable %s is empty", cmd.FromEnv)
		}
		return strings.NewReader(value + "\n"), nil
	}
	if cmd.UsernameFromEnv != "" || cmd.PasswordFromEnv != "" {
		username := os.Getenv(cmd.UsernameFromEnv)
		password := os.Getenv(cmd.PasswordFromEnv)
		if username == "" || password == "" {
			return nil, fmt.Errorf("auth: username/password environment variables must both be set")
		}
		return strings.NewReader(username + "\n" + password + "\n"), nil
	}
	return stdin, nil
}

func staticSecretLabels(cred tooldef.PackageCredential) []string {
	if cred.Type == "bearer" && cred.Inject.Method == "basic_auth" {
		return []string{"username", "password"}
	}
	switch cred.Type {
	case "api_key":
		return []string{"api_key"}
	case "bearer":
		return []string{"token"}
	default:
		return []string{"secret"}
	}
}

func selectAccountForMutation(ctx context.Context, repo *credentialrepo.Repository, pkg tooldef.Package, cred tooldef.PackageCredential, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	accounts, err := repo.DiscoverCredentialAccounts(ctx, pkg)
	if err != nil {
		return "", classifySecretStoreError(err)
	}
	list := accounts[cred.Name]
	if len(list) == 0 {
		return "default", nil
	}
	if len(list) > 1 {
		return "", stableAuthError("ambiguous_account", fmt.Sprintf("credential %s has multiple accounts (%s); specify --account", cred.Name, strings.Join(list, ", ")))
	}
	return list[0], nil
}

func ambiguousCredentialError(pkgName, workflow string, creds []tooldef.PackageCredential) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous_credential: package %q declares multiple %s credentials.\n\nCredentials:\n", pkgName, workflow)
	for _, cred := range creds {
		fmt.Fprintf(&b, "  %s\t%s\n", cred.Name, cred.Type)
	}
	fmt.Fprintln(&b, "\nChoose one with --credential.")
	return errors.New(strings.TrimSpace(b.String()))
}

func stableAuthError(state, message string) error { return fmt.Errorf("%s: %s", state, message) }

func authScopeError(err error) error {
	if strings.HasPrefix(err.Error(), "ambiguous_target:") {
		return err
	}
	return err
}

func classifySecretStoreError(err error) error {
	switch {
	case errors.Is(err, secrets.ErrNotInitialized):
		return stableAuthError("secret_store_uninitialized", "secret store is not initialized; run `toolbox auth setup` first")
	case errors.Is(err, secrets.ErrLocked):
		return stableAuthError("secret_store_locked", "secret store is locked; run `toolbox auth unlock` first")
	case errors.Is(err, secrets.ErrRecoveryRequired):
		return stableAuthError("secret_store_locked", "secret store was unlocked with a recovery code; run `toolbox auth recovery rewrap`")
	case errors.Is(err, secrets.ErrRecoveryWindowExpired):
		return stableAuthError("secret_store_locked", "secret-store recovery window expired; unlock with a recovery code again")
	default:
		return err
	}
}

func readPassphrase(stdin io.Reader, stdout io.Writer, prompt string) (string, error) {
	line, err := readLineWithPrompt(stdin, stdout, prompt)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return "", stableAuthError("secret_store_locked", "a passphrase or recovery code is required; non-interactive commands must provide it on stdin")
	}
	return line, nil
}

func readLineWithPrompt(stdin io.Reader, stdout io.Writer, prompt string) (string, error) {
	fmt.Fprint(stdout, prompt)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func confirm(stdin io.Reader, stdout io.Writer, prompt string) bool {
	fmt.Fprintf(stdout, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}

func installedContext(local bool) string {
	if local {
		return "override"
	}
	return "installed"
}

func packageContextLabel(pkg tooldef.Package, class string) string {
	return fmt.Sprintf("%s package %s (%s)", class, pkg.Name, pkg.Module)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
