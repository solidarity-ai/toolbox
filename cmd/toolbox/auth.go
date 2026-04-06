package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/oauth2flow"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
	"golang.org/x/oauth2"
)

// openBrowser opens a URL in the user's default browser.
// It is a variable so tests can replace it.
var openBrowser = func(url string) {
	_ = exec.Command("open", url).Start()
}

func runAuth(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Parse flags.
	check := false
	deleteCredential := false
	var account, credential, renameFrom, renameTo, deleteAccount string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			check = true
		case "--delete-credential":
			deleteCredential = true
		case "--account":
			if i+1 < len(args) {
				i++
				account = args[i]
			} else {
				return fmt.Errorf("auth: --account requires a value")
			}
		case "--credential":
			if i+1 < len(args) {
				i++
				credential = args[i]
			} else {
				return fmt.Errorf("auth: --credential requires a value")
			}
		case "--rename-account":
			if i+2 < len(args) {
				i++
				renameFrom = args[i]
				i++
				renameTo = args[i]
			} else {
				return fmt.Errorf("auth: --rename-account requires two values: <old-name> <new-name>")
			}
		case "--delete-account":
			if i+1 < len(args) {
				i++
				deleteAccount = args[i]
			} else {
				return fmt.Errorf("auth: --delete-account requires a value")
			}
		default:
			if strings.HasPrefix(args[i], "--account=") {
				account = strings.TrimPrefix(args[i], "--account=")
			} else if strings.HasPrefix(args[i], "--credential=") {
				credential = strings.TrimPrefix(args[i], "--credential=")
			} else if strings.HasPrefix(args[i], "--delete-account=") {
				deleteAccount = strings.TrimPrefix(args[i], "--delete-account=")
			} else {
				rest = append(rest, args[i])
			}
		}
	}

	opCount := 0
	if check {
		opCount++
	}
	if renameFrom != "" {
		opCount++
	}
	if deleteAccount != "" {
		opCount++
	}
	if deleteCredential {
		opCount++
	}
	if opCount > 1 {
		return fmt.Errorf("auth: choose only one of --check, --rename-account, --delete-account, or --delete-credential")
	}
	if deleteAccount != "" && account != "" {
		return fmt.Errorf("auth: use either --account or --delete-account, not both")
	}
	if deleteCredential && credential == "" {
		return fmt.Errorf("auth: --delete-credential requires --credential")
	}
	if deleteCredential && account != "" {
		return fmt.Errorf("auth: --delete-credential cannot be combined with --account")
	}

	if len(rest) == 0 {
		return fmt.Errorf("auth: expected package directory argument")
	}
	dir := rest[0]

	loaded, err := packaging.LoadDev(dir)
	if err != nil {
		return fmt.Errorf("auth: loading package from %s: %w", dir, err)
	}

	store := secrets.NewLocalSecretStore("", "")
	repo := credentialrepo.New(store)

	if renameFrom != "" {
		return renameAccountWithRepo(loaded, repo, credential, renameFrom, renameTo, stdout)
	}
	if deleteAccount != "" {
		return deleteAccountWithRepo(loaded, repo, credential, deleteAccount, stdout)
	}
	if deleteCredential {
		return deleteCredentialWithRepo(loaded, repo, credential, stdout)
	}

	if check {
		return checkAuthWithRepo(loaded, repo, stdout)
	}
	return runAuthWithRepo(loaded, repo, stdin, stdout, stderr, account, credential)
}

func runAuthWithRepo(loaded packaging.LoadedPackage, repo *credentialrepo.Repository, stdin io.Reader, stdout, stderr io.Writer, account, credential string) error {
	creds := loaded.Package.Credentials
	if len(creds) == 0 {
		fmt.Fprintln(stdout, "no credentials required")
		return nil
	}

	// Validate --account if provided.
	if account != "" {
		if err := transport.ValidateAccountString(account); err != nil {
			return fmt.Errorf("auth: invalid account name: %w", err)
		}
	}

	// Filter credentials by --credential flag if provided.
	if credential != "" {
		var filtered []tooldef.PackageCredential
		var names []string
		for _, c := range creds {
			names = append(names, c.Name)
			if c.Name == credential {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("auth: credential %q not found in package (available: %s)", credential, strings.Join(names, ", "))
		}
		creds = filtered
	}

	// Error on ambiguity: --account without --credential when 2+ credentials.
	if account != "" && credential == "" && len(creds) > 1 {
		var names []string
		for _, c := range creds {
			names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.Type))
		}
		return fmt.Errorf("auth: package has %d credentials (%s). Use --credential to specify which one", len(creds), strings.Join(names, ", "))
	}

	input := newAuthInput(stdin)
	ctx := context.Background()

	for _, cred := range creds {
		writeCredentialInstructions(stdout, cred, "")
		switch cred.Type {
		case "oauth2":
			if err := authOAuth2(ctx, repo, input, loaded.Package, cred, account, stdout, stderr); err != nil {
				return fmt.Errorf("auth: oauth2 credential %q: %w", cred.Name, err)
			}
		case "api_key":
			if err := authAPIKey(ctx, repo, input, loaded.Package, cred, account, stdout); err != nil {
				return fmt.Errorf("auth: api_key credential %q: %w", cred.Name, err)
			}
		case "bearer":
			if err := authBearer(ctx, repo, input, loaded.Package, cred, account, stdout); err != nil {
				return fmt.Errorf("auth: bearer credential %q: %w", cred.Name, err)
			}
		default:
			return fmt.Errorf("auth: unsupported credential type %q for %q", cred.Type, cred.Name)
		}
	}

	return nil
}

func authOAuth2(ctx context.Context, repo *credentialrepo.Repository, input *authInput, pkg tooldef.Package, cred tooldef.PackageCredential, account string, stdout, stderr io.Writer) error {
	if account == "" {
		account = "default"
	}
	pkceEnabled := cred.Provider.PKCEEnabled()
	clientIDKey := credentialrepo.OAuth2ClientIDRef(pkg, cred.Name)
	clientSecretKey := credentialrepo.OAuth2ClientSecretRef(pkg, cred.Name)
	refreshTokenKey := credentialrepo.OAuth2RefreshTokenRef(pkg, cred.Name, account)

	// Check if already configured — ask before re-authorizing.
	if _, err := getRequiredSecret(ctx, repo, refreshTokenKey); err == nil {
		fmt.Fprintf(stdout, "OAuth2 credentials for %s already configured. Re-authorize? (y/N): ", cred.Name)
		line, err := input.ReadLine(ctx)
		if err != nil {
			return nil
		}
		if strings.TrimSpace(strings.ToLower(line)) != "y" {
			fmt.Fprintf(stdout, "Skipping %s\n", cred.Name)
			return nil
		}
	}

	// Check secret store, then prompt for client_id (shared across accounts).
	clientID, err := getRequiredSecret(ctx, repo, clientIDKey)
	if err != nil {
		fmt.Fprintf(stdout, "Enter client_id for %s: ", cred.Name)
		line, err := input.ReadLine(ctx)
		if err != nil {
			return fmt.Errorf("reading client_id: %w", err)
		}
		clientID = []byte(line)
		if len(clientID) == 0 {
			return fmt.Errorf("client_id cannot be empty")
		}
		if err := repo.Set(ctx, clientIDKey, clientID); err != nil {
			return fmt.Errorf("storing client_id: %w", err)
		}
	}

	// Check secret store, then prompt for client_secret (shared across accounts).
	clientSecret, err := getOptionalSecret(ctx, repo, clientSecretKey)
	if err != nil {
		return err
	}
	if len(clientSecret) == 0 {
		prompt := fmt.Sprintf("Enter client_secret for %s: ", cred.Name)
		if pkceEnabled {
			prompt = fmt.Sprintf("Enter client_secret for %s (press Enter to skip for PKCE public clients): ", cred.Name)
		}
		fmt.Fprint(stdout, prompt)
		line, err := input.ReadLine(ctx)
		if err != nil {
			return fmt.Errorf("reading client_secret: %w", err)
		}
		clientSecret = []byte(line)
		if len(clientSecret) == 0 && !pkceEnabled {
			return fmt.Errorf("client_secret cannot be empty")
		}
		if len(clientSecret) > 0 {
			if err := repo.Set(ctx, clientSecretKey, clientSecret); err != nil {
				return fmt.Errorf("storing client_secret: %w", err)
			}
		}
	}

	// Resolve provider endpoints.
	authEndpoint, tokenEndpoint, err := resolveProviderEndpoints(cred)
	if err != nil {
		return err
	}

	cfg := &oauth2.Config{
		ClientID:     string(clientID),
		ClientSecret: string(clientSecret),
		Endpoint:     oauth2Endpoint(authEndpoint, tokenEndpoint, len(clientSecret) == 0),
		Scopes:       cred.Scopes,
	}

	// Build code receiver: callback server + manual paste, first wins.
	callbackRecv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		return fmt.Errorf("starting callback server: %w", err)
	}
	defer callbackRecv.Close()

	manualRecv := oauth2flow.NewManualReceiver(input, callbackRecv.RedirectURI())
	receiver := oauth2flow.NewRaceReceiver(callbackRecv.RedirectURI(), callbackRecv, manualRecv)
	defer receiver.Close()

	// Build auth options from manifest.
	verifier := ""
	if pkceEnabled {
		verifier = oauth2.GenerateVerifier()
	}
	var opts []oauth2.AuthCodeOption
	if cred.Provider != nil {
		for k, v := range cred.Provider.AuthParams {
			opts = append(opts, oauth2.SetAuthURLParam(k, v))
		}
	}

	flowCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	code, err := oauth2flow.AuthorizeCode(flowCtx, cfg, receiver, verifier, opts, func(authorizationURL string) {
		fmt.Fprintf(stdout, "Authorizing %s (oauth2)\n", cred.Name)
		if cred.Provider != nil && cred.Provider.Name != "" {
			fmt.Fprintf(stdout, "  Provider: %s\n", cred.Provider.Name)
		}
		if len(cred.Scopes) > 0 {
			fmt.Fprintf(stdout, "  Scopes:\n")
			for _, s := range cred.Scopes {
				fmt.Fprintf(stdout, "    - %s\n", s)
			}
		}
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "Opening browser to authorize...\n")
		fmt.Fprintf(stdout, "If the browser does not open, visit:\n%s\n", authorizationURL)
		fmt.Fprintf(stdout, "\nOr paste the authorization code here: ")
		openBrowser(authorizationURL)
	})
	if err != nil {
		return fmt.Errorf("oauth2 authorization: %w", err)
	}

	input.IgnoreOnce(code)

	var exchangeOpts []oauth2.AuthCodeOption
	if verifier != "" {
		exchangeOpts = append(exchangeOpts, oauth2.VerifierOption(verifier))
	}
	tok, err := cfg.Exchange(flowCtx, code, exchangeOpts...)
	if err != nil {
		return fmt.Errorf("oauth2 authorization: exchange token: %w", err)
	}

	if tok.RefreshToken == "" {
		return fmt.Errorf("token response missing refresh_token")
	}

	if err := repo.Set(ctx, refreshTokenKey, []byte(tok.RefreshToken)); err != nil {
		return fmt.Errorf("storing refresh_token: %w", err)
	}

	fmt.Fprintf(stdout, "Authorized %s (oauth2)\n", cred.Name)
	fmt.Fprintf(stdout, "  client_id:     secret store (%s)\n", clientIDKey.String())
	if len(clientSecret) > 0 {
		fmt.Fprintf(stdout, "  client_secret: secret store (%s)\n", clientSecretKey.String())
	} else {
		fmt.Fprintf(stdout, "  client_secret: not used (PKCE public client)\n")
	}
	fmt.Fprintf(stdout, "  refresh_token: secret store (%s)\n", refreshTokenKey.String())
	fmt.Fprintf(stdout, "\nCredentials stored. Tools in this package can now make authenticated requests.\n")

	return nil
}

func authAPIKey(ctx context.Context, repo *credentialrepo.Repository, input *authInput, pkg tooldef.Package, cred tooldef.PackageCredential, account string, stdout io.Writer) error {
	if account == "" {
		account = "default"
	}
	key := credentialrepo.APIKeyRef(pkg, cred.Name, account)

	if _, err := getRequiredSecret(ctx, repo, key); err == nil {
		fmt.Fprintf(stdout, "API key for %s already configured. Enter new value to overwrite, or press Enter to keep existing: ", cred.Name)
	} else {
		fmt.Fprintf(stdout, "Enter API key for %s: ", cred.Name)
	}
	value, err := input.ReadLine(ctx)
	if err != nil {
		return fmt.Errorf("reading api key: %w", err)
	}
	if value == "" {
		// If key already exists, keep existing value.
		if _, err := getRequiredSecret(ctx, repo, key); err == nil {
			fmt.Fprintf(stdout, "Keeping existing API key for %s\n", cred.Name)
			return nil
		}
		return fmt.Errorf("api key cannot be empty")
	}

	if err := repo.Set(ctx, key, []byte(value)); err != nil {
		return fmt.Errorf("storing api key: %w", err)
	}

	fmt.Fprintf(stdout, "Authorized %s (api_key)\n", cred.Name)
	fmt.Fprintf(stdout, "  api_key: secret store (%s)\n", key.String())
	fmt.Fprintf(stdout, "\nCredentials stored. Tools in this package can now make authenticated requests.\n")

	return nil
}

func authBearer(ctx context.Context, repo *credentialrepo.Repository, input *authInput, pkg tooldef.Package, cred tooldef.PackageCredential, account string, stdout io.Writer) error {
	if account == "" {
		account = "default"
	}

	if cred.Inject.Method == "basic_auth" {
		usernameKey := credentialrepo.BearerUsernameRef(pkg, cred.Name, account)
		passwordKey := credentialrepo.BearerPasswordRef(pkg, cred.Name, account)

		// Check if already configured.
		existingUsername, usrErr := getRequiredSecret(ctx, repo, usernameKey)
		if usrErr == nil {
			fmt.Fprintf(stdout, "Username for %s already configured. Enter new value to overwrite, or press Enter to keep existing: ", cred.Name)
		} else {
			fmt.Fprintf(stdout, "Enter username for %s: ", cred.Name)
		}
		username, err := input.ReadLine(ctx)
		if err != nil {
			return fmt.Errorf("reading username: %w", err)
		}
		switch {
		case username == "" && usrErr == nil:
			fmt.Fprintf(stdout, "Keeping existing username for %s\n", cred.Name)
			username = string(existingUsername)
		case username == "":
			return fmt.Errorf("username cannot be empty")
		}

		existingPassword, pwdErr := getRequiredSecret(ctx, repo, passwordKey)
		if pwdErr == nil {
			fmt.Fprintf(stdout, "Password for %s already configured. Enter new value to overwrite, or press Enter to keep existing: ", cred.Name)
		} else {
			fmt.Fprintf(stdout, "Enter password for %s: ", cred.Name)
		}
		password, err := input.ReadLine(ctx)
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		switch {
		case password == "" && pwdErr == nil:
			fmt.Fprintf(stdout, "Keeping existing password for %s\n", cred.Name)
			password = string(existingPassword)
		case password == "":
			return fmt.Errorf("password cannot be empty")
		}

		if err := repo.Set(ctx, usernameKey, []byte(username)); err != nil {
			return fmt.Errorf("storing username: %w", err)
		}
		if err := repo.Set(ctx, passwordKey, []byte(password)); err != nil {
			return fmt.Errorf("storing password: %w", err)
		}

		fmt.Fprintf(stdout, "Authorized %s (bearer)\n", cred.Name)
		fmt.Fprintf(stdout, "  username: secret store (%s)\n", usernameKey.String())
		fmt.Fprintf(stdout, "  password: secret store (%s)\n", passwordKey.String())
		fmt.Fprintf(stdout, "\nCredentials stored. Tools in this package can now make authenticated requests.\n")
	} else {
		tokenKey := credentialrepo.BearerTokenRef(pkg, cred.Name, account)

		if _, err := getRequiredSecret(ctx, repo, tokenKey); err == nil {
			fmt.Fprintf(stdout, "Token for %s already configured. Enter new value to overwrite, or press Enter to keep existing: ", cred.Name)
		} else {
			fmt.Fprintf(stdout, "Enter token for %s: ", cred.Name)
		}
		token, err := input.ReadLine(ctx)
		if err != nil {
			return fmt.Errorf("reading token: %w", err)
		}
		if token == "" {
			if _, err := getRequiredSecret(ctx, repo, tokenKey); err == nil {
				fmt.Fprintf(stdout, "Keeping existing token for %s\n", cred.Name)
				return nil
			}
			return fmt.Errorf("token cannot be empty")
		}
		if err := repo.Set(ctx, tokenKey, []byte(token)); err != nil {
			return fmt.Errorf("storing token: %w", err)
		}

		fmt.Fprintf(stdout, "Authorized %s (bearer)\n", cred.Name)
		fmt.Fprintf(stdout, "  token: secret store (%s)\n", tokenKey.String())
		fmt.Fprintf(stdout, "\nCredentials stored. Tools in this package can now make authenticated requests.\n")
	}

	return nil
}

func getRequiredSecret(ctx context.Context, repo *credentialrepo.Repository, ref credentialrepo.Ref) ([]byte, error) {
	value, err := getOptionalSecret(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	if len(value) == 0 {
		return nil, secrets.ErrNotFound
	}
	return value, nil
}

func getOptionalSecret(ctx context.Context, repo *credentialrepo.Repository, ref credentialrepo.Ref) ([]byte, error) {
	value, err := repo.Get(ctx, ref)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if len(value) == 0 {
		return nil, nil
	}
	return value, nil
}

func oauth2Endpoint(authURL, tokenURL string, publicClient bool) oauth2.Endpoint {
	endpoint := oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL}
	if publicClient {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	return endpoint
}

type authInput struct {
	requests    chan readRequest
	lines       chan authInputResult
	interactive bool
	mu          sync.Mutex
	ignore      map[string]int
}

func newAuthInput(reader io.Reader) *authInput {
	input := &authInput{
		requests:    make(chan readRequest),
		lines:       make(chan authInputResult),
		interactive: readerIsInteractiveTTY(reader),
		ignore:      make(map[string]int),
	}
	go input.pump(reader)
	go input.dispatch()
	return input
}

func (i *authInput) IgnoreOnce(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ignore[line]++
}

func (i *authInput) Read(_ []byte) (int, error) {
	return 0, fmt.Errorf("auth input does not support direct reads")
}

func (i *authInput) ReadLine(ctx context.Context) (string, error) {
	resp := make(chan authInputResult, 1)

	select {
	case i.requests <- readRequest{ctx: ctx, resp: resp}:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	select {
	case res := <-resp:
		return res.line, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type readRequest struct {
	ctx  context.Context
	resp chan authInputResult
}

type authInputResult struct {
	line string
	err  error
}

func (i *authInput) pump(reader io.Reader) {
	defer close(i.lines)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		i.lines <- authInputResult{line: strings.TrimSpace(scanner.Text())}
	}
	if err := scanner.Err(); err != nil {
		i.lines <- authInputResult{err: err}
	}
}

func (i *authInput) dispatch() {
	var (
		backlog  []authInputResult
		pending  *readRequest
		closed   bool
		closeErr error
		linesCh  = i.lines
	)

	for {
		if pending != nil && pending.ctx.Err() != nil {
			pending = nil
		}

		if pending != nil && len(backlog) > 0 {
			res := backlog[0]
			backlog = backlog[1:]
			pending.resp <- res
			pending = nil
			continue
		}

		if closed && pending != nil {
			pending.resp <- authInputResult{err: closeErr}
			pending = nil
			continue
		}

		select {
		case req := <-i.requests:
			if pending != nil && pending.ctx.Err() != nil {
				pending = nil
			}
			if closed && len(backlog) == 0 {
				req.resp <- authInputResult{err: closeErr}
				continue
			}
			if pending == nil {
				pending = &req
				continue
			}
			backlogReqErr := authInputResult{err: fmt.Errorf("auth input already has a pending reader")}
			req.resp <- backlogReqErr
		case line, ok := <-linesCh:
			if !ok {
				closed = true
				closeErr = io.EOF
				linesCh = nil
				continue
			}
			if line.err != nil {
				closed = true
				closeErr = line.err
				linesCh = nil
				continue
			}
			if i.shouldIgnore(line.line) {
				continue
			}
			if pending != nil && pending.ctx.Err() != nil {
				pending = nil
			}
			if pending != nil {
				pending.resp <- line
				pending = nil
				continue
			}
			if !i.interactive {
				backlog = append(backlog, line)
			}
		}
	}
}

func (i *authInput) shouldIgnore(line string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	if remaining := i.ignore[line]; remaining > 0 {
		if remaining == 1 {
			delete(i.ignore, line)
		} else {
			i.ignore[line] = remaining - 1
		}
		return true
	}
	return false
}

func readerIsInteractiveTTY(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func checkAuthWithRepo(loaded packaging.LoadedPackage, repo *credentialrepo.Repository, stdout io.Writer) error {
	creds := loaded.Package.Credentials
	if len(creds) == 0 {
		fmt.Fprintln(stdout, "no credentials required")
		return nil
	}

	ctx := context.Background()
	checks, err := repo.CheckPackage(ctx, loaded.Package)
	if err != nil {
		return err
	}

	for _, check := range checks.Credentials {
		if check.Configured() {
			fmt.Fprintf(stdout, "%s (%s): \u2713 configured\n", check.Credential.Name, check.Credential.Type)
		} else {
			fmt.Fprintf(stdout, "%s (%s): \u2717 not configured\n", check.Credential.Name, check.Credential.Type)
			writeCredentialInstructions(stdout, check.Credential, "  ")
		}
		for _, shared := range check.Shared {
			if shared.Present {
				fmt.Fprintf(stdout, "  %-14s \u2713 secret store\n", shared.Label+":")
			} else if !shared.Required {
				fmt.Fprintf(stdout, "  %-14s optional (PKCE public client)\n", shared.Label+":")
			} else {
				fmt.Fprintf(stdout, "  %-14s \u2717 missing \u2014 run 'toolbox auth %s'\n", shared.Label+":", loaded.Package.Name)
			}
		}
		for _, account := range check.Accounts {
			for _, secret := range account.Secrets {
				switch {
				case account.MissingAccount:
					fmt.Fprintf(stdout, "  %-14s \u2717 no accounts authorized \u2014 run 'toolbox auth %s'\n", secret.Label+":", loaded.Package.Name)
				case secret.Present:
					fmt.Fprintf(stdout, "  %-14s \u2713 secret store (account: %s)\n", secret.Label+":", account.Account)
				default:
					fmt.Fprintf(stdout, "  %-14s \u2717 missing for account %q \u2014 run 'toolbox auth %s --account %s'\n", secret.Label+":", account.Account, loaded.Package.Name, account.Account)
				}
			}
		}
	}

	return nil
}

func writeCredentialInstructions(stdout io.Writer, cred tooldef.PackageCredential, indent string) {
	instructions := strings.TrimSpace(cred.Instructions)
	if instructions == "" {
		return
	}
	fmt.Fprintf(stdout, "%sInstructions:\n", indent)
	for _, line := range strings.Split(instructions, "\n") {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(stdout)
			continue
		}
		fmt.Fprintf(stdout, "%s  %s\n", indent, line)
	}
	fmt.Fprintln(stdout)
}

func resolveProviderEndpoints(cred tooldef.PackageCredential) (authURL, tokenURL string, err error) {
	if cred.Provider == nil {
		return "", "", fmt.Errorf("oauth2 credential %q has no provider configured", cred.Name)
	}
	if cred.Provider.Name != "" {
		known, ok := transport.KnownProviders[cred.Provider.Name]
		if !ok {
			return "", "", fmt.Errorf("unknown provider %q", cred.Provider.Name)
		}
		return known.AuthURL, known.TokenURL, nil
	}
	if cred.Provider.AuthURL == "" || cred.Provider.TokenURL == "" {
		return "", "", fmt.Errorf("oauth2 credential %q requires auth_url and token_url", cred.Name)
	}
	return cred.Provider.AuthURL, cred.Provider.TokenURL, nil
}

func renameAccountWithRepo(loaded packaging.LoadedPackage, repo *credentialrepo.Repository, credentialName, oldName, newName string, stdout io.Writer) error {
	ctx := context.Background()
	results, err := repo.RenameAccount(ctx, loaded.Package, credentialName, oldName, newName)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "Renamed account %q → %q for credential %s (%d secrets moved)\n", oldName, newName, result.CredentialName, result.Moved)
	}
	return nil
}

func deleteAccountWithRepo(loaded packaging.LoadedPackage, repo *credentialrepo.Repository, credentialName, accountName string, stdout io.Writer) error {
	ctx := context.Background()
	results, err := repo.DeleteAccount(ctx, loaded.Package, credentialName, accountName)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if len(results) == 0 {
		if credentialName != "" {
			fmt.Fprintf(stdout, "No secrets found for account %q on credential %s\n", accountName, credentialName)
		} else {
			fmt.Fprintf(stdout, "No secrets found for account %q\n", accountName)
		}
		return nil
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "Deleted account %q for credential %s (%d secrets removed)\n", accountName, result.CredentialName, result.Deleted)
	}
	return nil
}

func deleteCredentialWithRepo(loaded packaging.LoadedPackage, repo *credentialrepo.Repository, credentialName string, stdout io.Writer) error {
	ctx := context.Background()
	result, err := repo.DeleteCredential(ctx, loaded.Package, credentialName)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if result.Deleted == 0 {
		fmt.Fprintf(stdout, "No secrets found for credential %s\n", result.CredentialName)
		return nil
	}
	fmt.Fprintf(stdout, "Deleted credential %s (%d secrets removed)\n", result.CredentialName, result.Deleted)
	return nil
}
