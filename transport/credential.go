package transport

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/secrets"
	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"
)

// CredentialType identifies the kind of credential to inject.
type CredentialType string

const (
	CredentialTypeOAuth2 CredentialType = "oauth2"
	CredentialTypeAPIKey CredentialType = "api_key"
	CredentialTypeBearer CredentialType = "bearer"
)

// InjectionMethod describes how a credential is attached to a request.
type InjectionMethod string

const (
	InjectionMethodBearerHeader InjectionMethod = "bearer_header"
	InjectionMethodBasicAuth    InjectionMethod = "basic_auth"
	InjectionMethodAPIKeyHeader InjectionMethod = "api_key_header"
	InjectionMethodAPIKeyQuery  InjectionMethod = "api_key_query"
)

// InjectionRule maps a request pattern to a credential source.
type InjectionRule struct {
	Hosts                    []string        `json:"hosts"`
	PathPrefix               string          `json:"path_prefix,omitempty"`
	ModuleName               string          `json:"module_name"`
	CredentialName           string          `json:"credential_name"`
	SecretPrefix             string          `json:"secret_prefix"`
	Type                     CredentialType  `json:"type"`
	Method                   InjectionMethod `json:"method"`
	HeaderName               string          `json:"header_name,omitempty"`
	AllowUnsafeHTTPInjection bool            `json:"allow_unsafe_http_injection,omitempty"`
	Provider                 *OAuth2Provider `json:"provider,omitempty"`
}

// OAuth2Provider holds OAuth2 endpoint configuration.
type OAuth2Provider struct {
	AuthURL  string `json:"auth_url"`
	TokenURL string `json:"token_url"`
}

// AppliedInjection records an exact credential mutation applied to a request so
// it can be removed or replaced on a later redirect hop.
type AppliedInjection struct {
	headerName        string
	headerValue       string
	queryName         string
	previousQueryVals []string
}

// KnownProviders maps well-known provider names to their OAuth2 endpoints.
var KnownProviders = map[string]OAuth2Provider{
	"google": {
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
	},
	"slack": {
		AuthURL:  "https://slack.com/oauth/v2/authorize",
		TokenURL: "https://slack.com/api/oauth.v2.access",
	},
	"microsoft": {
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	},
}

// CachedToken holds a cached access token with its expiry.
type CachedToken struct {
	AccessToken string
	ExpiresAt   time.Time
}

// TokenCache is a concurrent-safe in-memory token cache.
type TokenCache struct {
	mu     sync.RWMutex
	tokens map[string]CachedToken
}

// deniedProviderEndpoint identifies an exact OAuth provider endpoint that
// must never receive injected credentials.
type deniedProviderEndpoint struct {
	host string
	path string
}

// NewTokenCache returns an initialized TokenCache.
func NewTokenCache() *TokenCache {
	return &TokenCache{tokens: make(map[string]CachedToken)}
}

const expiryBuffer = 60 * time.Second

// Get returns the cached token if it exists and has not expired (with a 60s buffer).
func (c *TokenCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tok, ok := c.tokens[key]
	if !ok {
		return "", false
	}
	if time.Now().After(tok.ExpiresAt.Add(-expiryBuffer)) {
		return "", false
	}
	return tok.AccessToken, true
}

// Set stores a token in the cache.
func (c *TokenCache) Set(key string, token string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[key] = CachedToken{AccessToken: token, ExpiresAt: expiresAt}
}

// ValidateAccountString checks that an account name is safe for use in secret
// store key paths. It rejects empty strings, path traversal attempts, and
// unreasonably long values.
func ValidateAccountString(account string) error {
	if account == "" {
		return fmt.Errorf("account name cannot be empty")
	}
	if len(account) > 256 {
		return fmt.Errorf("account name too long (max 256 chars)")
	}
	if strings.Contains(account, "..") {
		return fmt.Errorf("account name cannot contain '..'")
	}
	if strings.Contains(account, "/") {
		return fmt.Errorf("account name cannot contain '/'")
	}
	if strings.ContainsRune(account, 0) {
		return fmt.Errorf("account name cannot contain null bytes")
	}
	return nil
}

// CredentialInjector injects credentials into outbound HTTP requests based on
// configured rules without exposing credentials to tool code.
type CredentialInjector struct {
	rules                   []InjectionRule
	deniedProviderEndpoints map[deniedProviderEndpoint]struct{}
	secrets                 secrets.SecretStore
	tokens                  *TokenCache
	refreshClient           *http.Client
	sf                      *singleflight.Group
}

// CredentialInjectorOption configures a CredentialInjector.
type CredentialInjectorOption func(*CredentialInjector)

// WithRefreshClient sets a custom HTTP client for OAuth2 token refresh requests.
func WithRefreshClient(c *http.Client) CredentialInjectorOption {
	return func(ci *CredentialInjector) {
		ci.refreshClient = c
	}
}

// NewCredentialInjector creates a CredentialInjector with the given rules and secret store.
func NewCredentialInjector(rules []InjectionRule, store secrets.SecretStore, opts ...CredentialInjectorOption) *CredentialInjector {
	ci := &CredentialInjector{
		rules:                   rules,
		deniedProviderEndpoints: buildDeniedProviderEndpoints(rules),
		secrets:                 store,
		tokens:                  NewTokenCache(),
		refreshClient:           &http.Client{Timeout: 10 * time.Second},
		sf:                      &singleflight.Group{},
	}
	for _, opt := range opts {
		opt(ci)
	}
	return ci
}

// WithAccounts returns a shallow copy of the injector with per-credential
// account scoping. The accounts map maps credential name to account name.
// Rules for matched credentials have their SecretPrefix adjusted to include
// the /accounts/{account}/ segment. Unmatched rules are copied unchanged.
// The TokenCache and singleflight.Group are shared (keys differ by prefix).
func (ci *CredentialInjector) WithAccounts(accounts map[string]string) (*CredentialInjector, error) {
	for cred, acct := range accounts {
		if err := ValidateAccountString(acct); err != nil {
			return nil, fmt.Errorf("credential %s: %w", cred, err)
		}
	}

	scopedRules := make([]InjectionRule, len(ci.rules))
	copy(scopedRules, ci.rules)
	for i, r := range scopedRules {
		if acct, ok := accounts[r.CredentialName]; ok {
			scopedRules[i].SecretPrefix = credpath.AccountPrefix(r.ModuleName, r.CredentialName, acct)
		}
	}

	return &CredentialInjector{
		rules:                   scopedRules,
		deniedProviderEndpoints: ci.deniedProviderEndpoints,
		secrets:                 ci.secrets,
		tokens:                  ci.tokens, // shared — keys differ by prefix
		refreshClient:           ci.refreshClient,
		sf:                      ci.sf, // shared — keys differ by prefix
	}, nil
}

func buildDeniedProviderEndpoints(rules []InjectionRule) map[deniedProviderEndpoint]struct{} {
	endpoints := make(map[deniedProviderEndpoint]struct{})
	for _, rule := range rules {
		if rule.Type != CredentialTypeOAuth2 || rule.Provider == nil {
			continue
		}
		for _, rawURL := range []string{rule.Provider.AuthURL, rule.Provider.TokenURL} {
			endpoint, ok := parseDeniedProviderEndpoint(rawURL)
			if !ok {
				continue
			}
			endpoints[endpoint] = struct{}{}
		}
	}
	return endpoints
}

func parseDeniedProviderEndpoint(rawURL string) (deniedProviderEndpoint, bool) {
	if rawURL == "" {
		return deniedProviderEndpoint{}, false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return deniedProviderEndpoint{}, false
	}
	return deniedProviderEndpoint{
		host: canonicalEndpointHost(u),
		path: canonicalEndpointPath(u),
	}, true
}

func canonicalEndpointHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" || port == defaultPortForScheme(u.Scheme) {
		return host
	}
	return net.JoinHostPort(host, port)
}

func canonicalEndpointPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func (ci *CredentialInjector) isDeniedProviderEndpoint(u *url.URL) bool {
	if u == nil || len(ci.deniedProviderEndpoints) == 0 {
		return false
	}
	endpoint := deniedProviderEndpoint{
		host: canonicalEndpointHost(u),
		path: canonicalEndpointPath(u),
	}
	_, denied := ci.deniedProviderEndpoints[endpoint]
	return denied
}

// Apply matches req against configured rules, injects the appropriate
// credential into the request, and returns a handle that can remove the exact
// applied mutation later.
func (ci *CredentialInjector) Apply(req *http.Request) (*AppliedInjection, error) {
	rule, cred, err := ci.matchedCredential(req.URL)
	if err != nil || rule == nil {
		return nil, err
	}
	return ci.applyToRequest(rule, req, cred), nil
}

// Remove removes the exact credential mutation previously applied to req.
func (a *AppliedInjection) Remove(req *http.Request) {
	if a == nil || req == nil {
		return
	}
	if a.headerName != "" {
		key := http.CanonicalHeaderKey(a.headerName)
		values := req.Header[key]
		if len(values) > 0 {
			filtered := make([]string, 0, len(values))
			removed := false
			for _, v := range values {
				if !removed && v == a.headerValue {
					removed = true
					continue
				}
				filtered = append(filtered, v)
			}
			if len(filtered) == 0 {
				req.Header.Del(key)
			} else {
				req.Header[key] = filtered
			}
		}
	}
	if a.queryName != "" && req.URL != nil {
		q := req.URL.Query()
		q.Del(a.queryName)
		for _, v := range a.previousQueryVals {
			q.Add(a.queryName, v)
		}
		req.URL.RawQuery = q.Encode()
	}
}

// InjectRequest matches the request URL against configured rules and injects
// the appropriate credential. It returns the (possibly modified) URL, headers,
// whether injection occurred, and any error.
func (ci *CredentialInjector) InjectRequest(method, rawURL string, headers [][2]string) (string, [][2]string, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, headers, false, fmt.Errorf("parsing URL: %w", err)
	}

	rule, cred, err := ci.matchedCredential(u)
	if err != nil || rule == nil {
		return rawURL, headers, false, err
	}

	newURL, newHeaders := ci.inject(rule, rawURL, u, headers, cred)
	return newURL, newHeaders, true, nil
}

// matchRule finds the first rule matching the given request URL.
func (ci *CredentialInjector) matchRule(u *url.URL) *InjectionRule {
	if ci.isDeniedProviderEndpoint(u) {
		return nil
	}
	host := u.Hostname()
	path := u.Path
	if path == "" {
		path = "/"
	}
	for i := range ci.rules {
		r := &ci.rules[i]
		if !ci.hostMatchesAny(r.Hosts, host) {
			continue
		}
		if r.PathPrefix != "" && !strings.HasPrefix(path, r.PathPrefix) {
			continue
		}
		return r
	}
	return nil
}

func (ci *CredentialInjector) matchedCredential(u *url.URL) (*InjectionRule, string, error) {
	rule := ci.matchRule(u)
	if rule == nil {
		return nil, "", nil
	}
	if strings.EqualFold(u.Scheme, "http") && !rule.AllowUnsafeHTTPInjection {
		return nil, "", fmt.Errorf("unsafe http credential injection blocked for %s", u.Host)
	}

	cred, err := ci.resolve(rule)
	if err != nil {
		return nil, "", fmt.Errorf("resolving credential for %s: %w", rule.ModuleName, err)
	}
	return rule, cred, nil
}

// hostMatchesAny checks if host matches any of the patterns.
func (ci *CredentialInjector) hostMatchesAny(patterns []string, host string) bool {
	for _, p := range patterns {
		if hostMatches(p, host) {
			return true
		}
	}
	return false
}

// hostMatches checks if host matches the pattern. Supports exact match and
// wildcard prefix like "*.googleapis.com".
func hostMatches(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // e.g. ".googleapis.com"
		return strings.HasSuffix(host, suffix)
	}
	return false
}

// resolve obtains the credential value for the given rule.
func (ci *CredentialInjector) resolve(rule *InjectionRule) (string, error) {
	switch rule.Type {
	case CredentialTypeOAuth2:
		return ci.resolveOAuth2(rule)
	case CredentialTypeBearer:
		return ci.resolveBearer(rule)
	case CredentialTypeAPIKey:
		return ci.resolveSecret(rule, "api_key")
	default:
		return "", fmt.Errorf("unknown credential type: %s", rule.Type)
	}
}

// resolveBearer resolves a bearer credential. For basic auth, it reads
// username and password and returns the base64-encoded "user:pass" string.
// For other methods, it reads a token directly.
func (ci *CredentialInjector) resolveBearer(rule *InjectionRule) (string, error) {
	if rule.Method == InjectionMethodBasicAuth {
		username, err := ci.resolveSecret(rule, "username")
		if err != nil {
			return "", err
		}
		password, err := ci.resolveSecret(rule, "password")
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString([]byte(username + ":" + password)), nil
	}
	return ci.resolveSecret(rule, "token")
}

// resolveSecret reads a secret directly from the store.
func (ci *CredentialInjector) resolveSecret(rule *InjectionRule, suffix string) (string, error) {
	key := rule.SecretPrefix + suffix
	val, err := ci.secrets.Get(context.Background(), key)
	if err != nil {
		return "", fmt.Errorf("secret %s not found — run 'toolbox auth' for package %q to set up credentials: %w", key, rule.ModuleName, err)
	}
	return string(val), nil
}

// resolveOAuth2 returns a valid access token, refreshing if needed.
// Uses singleflight to deduplicate concurrent refresh requests.
func (ci *CredentialInjector) resolveOAuth2(rule *InjectionRule) (string, error) {
	cacheKey := rule.SecretPrefix

	if tok, ok := ci.tokens.Get(cacheKey); ok {
		return tok, nil
	}

	val, err, _ := ci.sf.Do(cacheKey, func() (interface{}, error) {
		// Double-check after acquiring the singleflight slot.
		if tok, ok := ci.tokens.Get(cacheKey); ok {
			return tok, nil
		}
		return ci.refreshOAuth2(rule, cacheKey)
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// refreshOAuth2 performs the OAuth2 refresh token flow using golang.org/x/oauth2.
func (ci *CredentialInjector) refreshOAuth2(rule *InjectionRule, cacheKey string) (string, error) {
	if rule.Provider == nil {
		return "", fmt.Errorf("oauth2 rule for %s has no provider configured", rule.ModuleName)
	}

	ctx := context.Background()
	if ci.refreshClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, ci.refreshClient)
	}

	// Client credentials are shared (stored at pkg/cred/, not per-account).
	clientIDKey := credpath.OAuth2ClientID(rule.ModuleName, rule.CredentialName)
	clientID, err := ci.secrets.Get(ctx, clientIDKey)
	if err != nil {
		return "", fmt.Errorf("secret %s not found — run 'toolbox auth' for package %q to set up credentials: %w", clientIDKey, rule.ModuleName, err)
	}
	clientSecretKey := credpath.OAuth2ClientSecret(rule.ModuleName, rule.CredentialName)
	clientSecret, err := ci.secrets.Get(ctx, clientSecretKey)
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return "", fmt.Errorf("reading secret %s for package %q: %w", clientSecretKey, rule.ModuleName, err)
	}
	if errors.Is(err, secrets.ErrNotFound) || len(clientSecret) == 0 {
		clientSecret = nil
	}
	// Refresh token is per-account (SecretPrefix is already scoped to the account).
	refreshTokenKey := rule.SecretPrefix + "refresh_token"
	refreshToken, err := ci.secrets.Get(ctx, refreshTokenKey)
	if err != nil {
		return "", fmt.Errorf("secret %s not found — run 'toolbox auth' for package %q to set up credentials: %w", refreshTokenKey, rule.ModuleName, err)
	}

	endpoint := oauth2.Endpoint{TokenURL: rule.Provider.TokenURL}
	if len(clientSecret) == 0 {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	cfg := &oauth2.Config{
		ClientID:     string(clientID),
		ClientSecret: string(clientSecret),
		Endpoint:     endpoint,
	}

	tok, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: string(refreshToken)}).Token()
	if err != nil {
		return "", fmt.Errorf("token refresh failed for %s — run 'toolbox auth' to re-authorize: %w", rule.ModuleName, err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != string(refreshToken) {
		if err := ci.secrets.Set(ctx, refreshTokenKey, []byte(tok.RefreshToken)); err != nil {
			return "", fmt.Errorf("persist rotated refresh token for %s: %w", rule.ModuleName, err)
		}
	}

	ci.tokens.Set(cacheKey, tok.AccessToken, tok.Expiry)

	return tok.AccessToken, nil
}

// StripInjected removes any credentials from req that this injector may have
// added, unless the destination matches an injection rule (meaning the
// credentials are intended for that host). Call on non-redirected requests or
// when the redirect provenance is not available.
func (ci *CredentialInjector) StripInjected(req *http.Request) {
	if ci.matchRule(req.URL) != nil {
		return
	}
	ci.stripAllInjected(req)
}

func (ci *CredentialInjector) stripAllInjected(req *http.Request) {
	stripped := make(map[string]bool)
	for _, rule := range ci.rules {
		key := string(rule.Method) + "\x00" + rule.HeaderName
		if stripped[key] {
			continue
		}
		stripped[key] = true
		switch rule.Method {
		case InjectionMethodBearerHeader, InjectionMethodBasicAuth:
			req.Header.Del("Authorization")
		case InjectionMethodAPIKeyHeader:
			req.Header.Del(rule.apiKeyHeaderName())
		case InjectionMethodAPIKeyQuery:
			q := req.URL.Query()
			q.Del("key")
			req.URL.RawQuery = q.Encode()
		}
	}
}

// apiKeyHeaderName returns the header name for api_key_header injection,
// defaulting to X-API-Key if not configured.
func (r *InjectionRule) apiKeyHeaderName() string {
	if r.HeaderName != "" {
		return r.HeaderName
	}
	return "X-API-Key"
}

// inject applies the credential to the request using the specified method.
func (ci *CredentialInjector) inject(rule *InjectionRule, rawURL string, u *url.URL, headers [][2]string, cred string) (string, [][2]string) {
	switch rule.Method {
	case InjectionMethodBearerHeader:
		headers = append(headers, [2]string{"Authorization", "Bearer " + cred})
	case InjectionMethodBasicAuth:
		headers = append(headers, [2]string{"Authorization", "Basic " + cred})
	case InjectionMethodAPIKeyHeader:
		headers = append(headers, [2]string{rule.apiKeyHeaderName(), cred})
	case InjectionMethodAPIKeyQuery:
		q := u.Query()
		q.Set("key", cred)
		u.RawQuery = q.Encode()
		rawURL = u.String()
	}
	return rawURL, headers
}

func (ci *CredentialInjector) applyToRequest(rule *InjectionRule, req *http.Request, cred string) *AppliedInjection {
	switch rule.Method {
	case InjectionMethodBearerHeader:
		value := "Bearer " + cred
		req.Header.Add("Authorization", value)
		return &AppliedInjection{headerName: "Authorization", headerValue: value}
	case InjectionMethodBasicAuth:
		value := "Basic " + cred
		req.Header.Add("Authorization", value)
		return &AppliedInjection{headerName: "Authorization", headerValue: value}
	case InjectionMethodAPIKeyHeader:
		headerName := rule.apiKeyHeaderName()
		req.Header.Add(headerName, cred)
		return &AppliedInjection{headerName: headerName, headerValue: cred}
	case InjectionMethodAPIKeyQuery:
		q := req.URL.Query()
		prev := append([]string(nil), q["key"]...)
		q.Set("key", cred)
		req.URL.RawQuery = q.Encode()
		return &AppliedInjection{queryName: "key", previousQueryVals: prev}
	default:
		return nil
	}
}
