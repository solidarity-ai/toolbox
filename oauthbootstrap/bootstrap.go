package oauthbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	defaultCallbackHost    = "127.0.0.1"
	defaultCallbackPath    = "/oauth/callback"
	defaultCallbackTimeout = 2 * time.Minute
	defaultExchangeTimeout = 10 * time.Second
)

type BrowserOpener func(context.Context, string) error

type ListenerFactory func(network, address string) (net.Listener, error)

type Bootstrapper struct {
	store           secrets.SecretStore
	httpClient      *http.Client
	openBrowser     BrowserOpener
	listen          ListenerFactory
	randReader      ioReader
	callbackTimeout time.Duration
	exchangeTimeout time.Duration
	callbackHost    string
	callbackPath    string
}

type ioReader interface {
	Read(p []byte) (n int, err error)
}

type Options struct {
	Store           secrets.SecretStore
	HTTPClient      *http.Client
	OpenBrowser     BrowserOpener
	Listen          ListenerFactory
	RandReader      ioReader
	CallbackTimeout time.Duration
	ExchangeTimeout time.Duration
	CallbackHost    string
	CallbackPath    string
}

type Request struct {
	Package        packaging.LoadedPackage
	CredentialName string
	Tenant         string
	ClientID       string
	ClientSecret   string
}

type Result struct {
	CredentialName   string
	RedirectURI      string
	AuthorizationURL string
	SecretNamespace  string
	PersistedKeys    []string
	UsedPKCE         bool
}

type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("oauth bootstrap failed during %s: %v", e.Stage, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

func New(opts Options) *Bootstrapper {
	callbackHost := strings.TrimSpace(opts.CallbackHost)
	if callbackHost == "" {
		callbackHost = defaultCallbackHost
	}
	callbackPath := strings.TrimSpace(opts.CallbackPath)
	if callbackPath == "" {
		callbackPath = defaultCallbackPath
	}
	callbackTimeout := opts.CallbackTimeout
	if callbackTimeout <= 0 {
		callbackTimeout = defaultCallbackTimeout
	}
	exchangeTimeout := opts.ExchangeTimeout
	if exchangeTimeout <= 0 {
		exchangeTimeout = defaultExchangeTimeout
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: exchangeTimeout}
	}
	listen := opts.Listen
	if listen == nil {
		listen = net.Listen
	}
	return &Bootstrapper{
		store:           opts.Store,
		httpClient:      client,
		openBrowser:     opts.OpenBrowser,
		listen:          listen,
		randReader:      opts.RandReader,
		callbackTimeout: callbackTimeout,
		exchangeTimeout: exchangeTimeout,
		callbackHost:    callbackHost,
		callbackPath:    callbackPath,
	}
}

func (b *Bootstrapper) Run(ctx context.Context, req Request) (Result, error) {
	if b == nil {
		b = New(Options{})
	}
	selection, err := selectCredential(req.Package, req.CredentialName)
	if err != nil {
		return Result{}, &StageError{Stage: "credential selection", Err: err}
	}
	if b.store == nil {
		return Result{}, &StageError{Stage: "secret persistence", Err: fmt.Errorf("secret store is required")}
	}
	if b.openBrowser == nil {
		return Result{}, &StageError{Stage: "browser launch", Err: fmt.Errorf("browser opener is required")}
	}

	listener, err := b.listen("tcp", net.JoinHostPort(b.callbackHost, "0"))
	if err != nil {
		return Result{}, &StageError{Stage: "callback wait", Err: fmt.Errorf("listen on localhost callback: %w", err)}
	}
	defer listener.Close()

	redirectURI, err := b.redirectURI(listener)
	if err != nil {
		return Result{}, &StageError{Stage: "callback wait", Err: err}
	}

	state, err := newState(b.randReader)
	if err != nil {
		return Result{}, &StageError{Stage: "callback validation", Err: fmt.Errorf("generate callback state: %w", err)}
	}

	usedPKCE := strings.TrimSpace(req.ClientSecret) == ""
	verifier := ""
	challenge := ""
	if usedPKCE {
		verifier, err = newPKCEVerifier(b.randReader)
		if err != nil {
			return Result{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("generate pkce verifier: %w", err)}
		}
		challenge = pkceChallenge(verifier)
	}

	authorizationURL, err := buildAuthorizationURL(selection.provider, selection.credential, strings.TrimSpace(req.ClientID), redirectURI, state, challenge)
	if err != nil {
		return Result{}, &StageError{Stage: "credential selection", Err: err}
	}

	callbackCtx, cancelCallback := context.WithTimeout(ctx, b.callbackTimeout)
	defer cancelCallback()
	callbackResultCh := make(chan callbackResult, 1)
	server := &http.Server{Handler: callbackHandler(selection.credential.Name, state, callbackResultCh)}
	serveErrCh := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		select {
		case <-serveErrCh:
		default:
		}
	}()

	if err := b.openBrowser(ctx, authorizationURL); err != nil {
		return Result{}, &StageError{Stage: "browser launch", Err: fmt.Errorf("open authorization url for credential %q: %w", selection.credential.Name, err)}
	}

	callback, err := waitForCallback(callbackCtx, redirectURI, callbackResultCh, serveErrCh)
	if err != nil {
		return Result{}, err
	}

	tokens, err := b.exchangeCode(ctx, exchangeRequest{
		Provider:     selection.provider,
		Code:         callback.Code,
		RedirectURI:  redirectURI,
		ClientID:     strings.TrimSpace(req.ClientID),
		ClientSecret: strings.TrimSpace(req.ClientSecret),
		Verifier:     verifier,
		Credential:   selection.credential.Name,
	})
	if err != nil {
		return Result{}, err
	}

	persistedKeys, namespace, err := persistDurableSecrets(ctx, b.store, selection.module, req.Tenant, selection.credential.Name, strings.TrimSpace(req.ClientID), strings.TrimSpace(req.ClientSecret), tokens.RefreshToken)
	if err != nil {
		return Result{}, err
	}

	return Result{
		CredentialName:   selection.credential.Name,
		RedirectURI:      redirectURI,
		AuthorizationURL: authorizationURL,
		SecretNamespace:  namespace,
		PersistedKeys:    persistedKeys,
		UsedPKCE:         usedPKCE,
	}, nil
}

type selectedCredential struct {
	module     tooldef.ModulePath
	credential tooldef.PackageCredential
	provider   tooldef.OAuth2ProviderConfig
}

func selectCredential(pkg packaging.LoadedPackage, explicit string) (selectedCredential, error) {
	module := strings.TrimSpace(pkg.Package.Module.String())
	if module == "" {
		return selectedCredential{}, fmt.Errorf("package module identity is required")
	}
	parsedModule, err := tooldef.ParseModulePath(module)
	if err != nil {
		return selectedCredential{}, fmt.Errorf("package module identity %q is invalid: %w", module, err)
	}

	explicit = strings.TrimSpace(explicit)
	oauthCreds := make([]tooldef.PackageCredential, 0, len(pkg.Package.Credentials))
	for _, credential := range pkg.Package.Credentials {
		if credential.Type == tooldef.CredentialTypeOAuth2 {
			oauthCreds = append(oauthCreds, credential)
		}
	}
	if len(oauthCreds) == 0 {
		return selectedCredential{}, fmt.Errorf("package %q declares no oauth2 credentials", pkg.Package.Name)
	}

	if explicit == "" && len(oauthCreds) > 1 {
		names := make([]string, 0, len(oauthCreds))
		for _, credential := range oauthCreds {
			names = append(names, credential.Name)
		}
		sort.Strings(names)
		return selectedCredential{}, fmt.Errorf("package %q declares multiple oauth2 credentials; choose one of %s", pkg.Package.Name, strings.Join(names, ", "))
	}

	var chosen *tooldef.PackageCredential
	for idx := range oauthCreds {
		credential := oauthCreds[idx]
		if explicit == "" || credential.Name == explicit {
			chosen = &credential
			if explicit != "" {
				break
			}
		}
	}
	if chosen == nil {
		return selectedCredential{}, fmt.Errorf("oauth2 credential %q was not found in package %q", explicit, pkg.Package.Name)
	}
	if strings.TrimSpace(chosen.Name) == "" {
		return selectedCredential{}, fmt.Errorf("oauth2 credential name is required")
	}
	if len(chosen.Scopes) == 0 {
		return selectedCredential{}, fmt.Errorf("oauth2 credential %q must declare at least one scope", chosen.Name)
	}
	provider, err := tooldef.ResolveOAuth2Provider(chosen.Provider)
	if err != nil {
		return selectedCredential{}, fmt.Errorf("oauth2 credential %q provider: %w", chosen.Name, err)
	}
	return selectedCredential{module: parsedModule, credential: *chosen, provider: provider}, nil
}

func (b *Bootstrapper) redirectURI(listener net.Listener) (string, error) {
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("callback listener returned unsupported address type %T", listener.Addr())
	}
	path := b.callbackPath
	if path == "" {
		path = defaultCallbackPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(b.callbackHost, strconv.Itoa(addr.Port)), Path: path}).String(), nil
}

func buildAuthorizationURL(provider tooldef.OAuth2ProviderConfig, credential tooldef.PackageCredential, clientID string, redirectURI string, state string, challenge string) (string, error) {
	if strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("client_id must not be empty")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return "", fmt.Errorf("redirect uri is required")
	}
	authURL, err := url.Parse(provider.AuthURL)
	if err != nil {
		return "", fmt.Errorf("parse auth url: %w", err)
	}
	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("scope", strings.Join(credential.Scopes, " "))
	if strings.TrimSpace(challenge) != "" {
		query.Set("code_challenge", challenge)
		query.Set("code_challenge_method", pkceChallengeMethod)
	}
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

type callbackResult struct {
	Code string
	Err  error
}

func callbackHandler(credentialName string, expectedState string, results chan<- callbackResult) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := validateCallback(r, expectedState)
		once.Do(func() {
			results <- result
		})
		if result.Err != nil {
			http.Error(w, "OAuth callback rejected.", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Authorization received. You can close this window."))
	})
}

func validateCallback(r *http.Request, expectedState string) callbackResult {
	query := r.URL.Query()
	state := strings.TrimSpace(query.Get("state"))
	if state == "" {
		return callbackResult{Err: fmt.Errorf("callback missing state")}
	}
	if state != expectedState {
		return callbackResult{Err: fmt.Errorf("callback state mismatch")}
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		return callbackResult{Err: fmt.Errorf("callback missing code")}
	}
	return callbackResult{Code: code}
}

func waitForCallback(ctx context.Context, redirectURI string, results <-chan callbackResult, serveErrs <-chan error) (callbackResult, error) {
	for {
		select {
		case <-ctx.Done():
			return callbackResult{}, &StageError{Stage: "callback wait", Err: fmt.Errorf("timed out waiting for localhost callback at %s: %w", redirectURI, ctx.Err())}
		case result := <-results:
			if result.Err != nil {
				return callbackResult{}, &StageError{Stage: "callback validation", Err: result.Err}
			}
			return result, nil
		case err := <-serveErrs:
			if err != nil {
				return callbackResult{}, &StageError{Stage: "callback wait", Err: fmt.Errorf("callback server failed for %s: %w", redirectURI, err)}
			}
		}
	}
}

type exchangeRequest struct {
	Provider     tooldef.OAuth2ProviderConfig
	Code         string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	Verifier     string
	Credential   string
}

type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
}

func (b *Bootstrapper) exchangeCode(ctx context.Context, req exchangeRequest) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("redirect_uri", req.RedirectURI)
	form.Set("client_id", req.ClientID)
	if req.ClientSecret != "" {
		form.Set("client_secret", req.ClientSecret)
	}
	if req.Verifier != "" {
		form.Set("code_verifier", req.Verifier)
	}

	exchangeCtx, cancel := context.WithTimeout(ctx, b.exchangeTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(exchangeCtx, http.MethodPost, req.Provider.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("build exchange request for credential %q: %w", req.Credential, err)}
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("exchange authorization code for credential %q: %w", req.Credential, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("provider token endpoint for credential %q returned status %d", req.Credential, resp.StatusCode)}
	}

	var payload tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("parse token response for credential %q: %w", req.Credential, err)}
	}
	if strings.TrimSpace(payload.RefreshToken) == "" {
		return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("token response for credential %q is missing refresh_token", req.Credential)}
	}
	if len(payload.ExpiresIn) > 0 && strings.TrimSpace(string(payload.ExpiresIn)) != "null" {
		if _, err := parseExpiresIn(payload.ExpiresIn); err != nil {
			return tokenResponse{}, &StageError{Stage: "token exchange", Err: fmt.Errorf("token response for credential %q has malformed expires_in: %w", req.Credential, err)}
		}
	}
	return payload, nil
}

func parseExpiresIn(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		value, err := number.Int64()
		if err != nil {
			return 0, fmt.Errorf("expires_in must be an integer")
		}
		return value, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, err := strconv.ParseInt(strings.TrimSpace(asString), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("expires_in must be numeric")
		}
		return value, nil
	}
	return 0, fmt.Errorf("expires_in must be numeric")
}

func persistDurableSecrets(ctx context.Context, store secrets.SecretStore, module tooldef.ModulePath, tenant string, credentialName string, clientID string, clientSecret string, refreshToken string) ([]string, string, error) {
	namespace, err := tooldef.CredentialFamilyNamespace(module, tenant, credentialName)
	if err != nil {
		return nil, "", &StageError{Stage: "secret persistence", Err: err}
	}
	writes := make([]secretWrite, 0, 3)
	for family, value := range map[string]string{
		"client_id":     clientID,
		"refresh_token": refreshToken,
	} {
		key, err := tooldef.CredentialFamilyMemberKey(namespace, family)
		if err != nil {
			return nil, "", &StageError{Stage: "secret persistence", Err: err}
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, "", &StageError{Stage: "secret persistence", Err: fmt.Errorf("refusing to store empty secret value for %s", key)}
		}
		writes = append(writes, secretWrite{Key: key, Value: trimmed})
	}
	if strings.TrimSpace(clientSecret) != "" {
		key, err := tooldef.CredentialFamilyMemberKey(namespace, "client_secret")
		if err != nil {
			return nil, "", &StageError{Stage: "secret persistence", Err: err}
		}
		writes = append(writes, secretWrite{Key: key, Value: strings.TrimSpace(clientSecret)})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Key < writes[j].Key })

	originals := make(map[string]secretSnapshot, len(writes))
	for _, write := range writes {
		value, err := store.Get(ctx, write.Key)
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				originals[write.Key] = secretSnapshot{}
				continue
			}
			return nil, "", &StageError{Stage: "secret persistence", Err: fmt.Errorf("read existing durable secret %s: %w", write.Key, err)}
		}
		originals[write.Key] = secretSnapshot{Present: true, Value: append([]byte(nil), value...)}
	}

	applied := make([]secretWrite, 0, len(writes))
	for _, write := range writes {
		if err := store.Set(ctx, write.Key, []byte(write.Value)); err != nil {
			rollbackErr := rollbackWrites(ctx, store, applied, originals)
			if rollbackErr != nil {
				return nil, "", &StageError{Stage: "secret persistence", Err: fmt.Errorf("write durable secret %s: %v (rollback failed: %v)", write.Key, err, rollbackErr)}
			}
			return nil, "", &StageError{Stage: "secret persistence", Err: fmt.Errorf("write durable secret %s: %w", write.Key, err)}
		}
		applied = append(applied, write)
	}

	persisted := make([]string, 0, len(writes))
	for _, write := range writes {
		persisted = append(persisted, write.Key)
	}
	return persisted, namespace, nil
}

type secretWrite struct {
	Key   string
	Value string
}

type secretSnapshot struct {
	Present bool
	Value   []byte
}

func rollbackWrites(ctx context.Context, store secrets.SecretStore, applied []secretWrite, originals map[string]secretSnapshot) error {
	for idx := len(applied) - 1; idx >= 0; idx-- {
		write := applied[idx]
		original := originals[write.Key]
		if !original.Present {
			err := store.Delete(ctx, write.Key)
			if err != nil && !errors.Is(err, secrets.ErrNotFound) {
				return err
			}
			continue
		}
		if err := store.Set(ctx, write.Key, original.Value); err != nil {
			return err
		}
	}
	return nil
}
