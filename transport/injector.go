package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/audit"
	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"
)

const (
	exactHostBonus        = 1000
	defaultRefreshTimeout = 10 * time.Second
	oauth2ExpiryBuffer    = 60 * time.Second
)

// Rule is the transport-owned runtime rule for simple credential injection.
// It contains only runtime metadata needed for matching and request mutation.
type Rule struct {
	Name               string
	SecretKey          string
	Type               tooldef.CredentialType
	OAuth2Provider     *tooldef.OAuth2ProviderConfig
	OAuth2SecretFamily string
	OAuth2CacheKey     string
	Scopes             []string
	Inject             tooldef.CredentialInject

	hostMatchers []hostMatcher
	pathPrefix   string
}

// Injector validates, matches, and injects simple transport credentials.
type Injector struct {
	store         secrets.SecretStore
	rules         []Rule
	now           func() time.Time
	refreshClient *http.Client
	auditSink     audit.Sink

	cacheMu       sync.RWMutex
	oauth2Cache   map[string]oauth2CachedToken
	oauth2Refresh singleflight.Group
}

type InjectorOption func(*Injector)

type hostMatcher struct {
	pattern string
	suffix  string
	exact   bool
}

type matchScore struct {
	hostScore int
	pathScore int
}

type oauth2CachedToken struct {
	accessToken string
	expiresAt   time.Time
}

type oauth2TokenResponse struct {
	AccessToken string          `json:"access_token"`
	ExpiresIn   json.RawMessage `json:"expires_in"`
}

type oauth2CaptureTransport struct {
	base       http.RoundTripper
	lastStatus int
	lastBody   []byte
}

// WithClock injects a deterministic clock for cache-expiry testing.
func WithClock(now func() time.Time) InjectorOption {
	return func(i *Injector) {
		if now != nil {
			i.now = now
		}
	}
}

// WithRefreshHTTPClient injects the direct OAuth2 refresh client.
func WithRefreshHTTPClient(client *http.Client) InjectorOption {
	return func(i *Injector) {
		if client != nil {
			i.refreshClient = client
		}
	}
}

// WithAuditSink injects a best-effort audit sink for transport events.
func WithAuditSink(sink audit.Sink) InjectorOption {
	return func(i *Injector) {
		i.auditSink = audit.SinkOrNoop(sink)
	}
}

// NewInjector canonicalizes and validates the provided rules up front so live
// requests only evaluate deterministic match state.
func NewInjector(store secrets.SecretStore, rules []Rule) (*Injector, error) {
	return NewInjectorWithOptions(store, rules)
}

// NewInjectorWithOptions canonicalizes and validates the provided rules up
// front so live requests only evaluate deterministic match state.
func NewInjectorWithOptions(store secrets.SecretStore, rules []Rule, opts ...InjectorOption) (*Injector, error) {
	injector := &Injector{
		store:         store,
		now:           time.Now,
		refreshClient: &http.Client{Timeout: defaultRefreshTimeout},
		auditSink:     audit.NoopSink{},
		oauth2Cache:   make(map[string]oauth2CachedToken),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(injector)
		}
	}

	if len(rules) == 0 {
		return injector, nil
	}

	normalized := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		nr, err := normalizeRule(rule)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, nr)
	}

	if err := rejectAmbiguousRules(normalized); err != nil {
		return nil, err
	}

	injector.rules = normalized
	return injector, nil
}

// Rules returns a snapshot of the normalized runtime rules.
func (i *Injector) Rules() []Rule {
	if i == nil || len(i.rules) == 0 {
		return nil
	}
	out := make([]Rule, len(i.rules))
	copy(out, i.rules)
	for idx := range out {
		out[idx].Scopes = append([]string(nil), out[idx].Scopes...)
		out[idx].Inject.Hosts = append([]string(nil), out[idx].Inject.Hosts...)
		if out[idx].OAuth2Provider != nil {
			provider := *out[idx].OAuth2Provider
			out[idx].OAuth2Provider = &provider
		}
		out[idx].hostMatchers = nil
	}
	return out
}

// InjectRequest applies transport-managed auth to a request represented as a
// URL plus Fetch-style headers. It mutates headers and may return a rewritten URL.
func (i *Injector) InjectRequest(ctx context.Context, rawURL string, headers *fetch.Headers) (string, error) {
	if i == nil || len(i.rules) == 0 {
		return rawURL, nil
	}

	parsed, err := parseRequestURL(rawURL)
	if err != nil {
		return rawURL, err
	}

	rule, ok, err := i.matchRule(parsed)
	if err != nil {
		return rawURL, err
	}
	if !ok {
		return rawURL, nil
	}
	if i.store == nil {
		return rawURL, fmt.Errorf("transport auth configured for %q but no secret store is available", rule.SecretKey)
	}

	value, err := i.resolveSecretMaterial(ctx, rule)
	if err != nil {
		return rawURL, err
	}

	switch rule.Inject.Method {
	case "bearer_header":
		return i.injectBearerHeader(rawURL, headers, rule, value)
	case "basic_auth":
		return i.injectBasicAuth(rawURL, headers, rule, value)
	case "api_key_header":
		return i.injectAPIKeyHeader(rawURL, headers, rule, value)
	case "api_key_query":
		return i.injectAPIKeyQuery(parsed, rule, value)
	default:
		return rawURL, fmt.Errorf("credential %q uses unsupported injection method %q", rule.Name, rule.Inject.Method)
	}
}

func (i *Injector) resolveSecretMaterial(ctx context.Context, rule Rule) (string, error) {
	if rule.Type == tooldef.CredentialTypeOAuth2 && rule.OAuth2Provider != nil && rule.OAuth2SecretFamily != "" && rule.OAuth2CacheKey != "" {
		return i.resolveOAuth2AccessToken(ctx, rule)
	}
	secret, err := i.store.Get(ctx, rule.SecretKey)
	if err != nil {
		return "", fmt.Errorf("resolve transport credential %q: %w", rule.SecretKey, err)
	}
	value := string(secret)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("transport credential %q resolved unusable secret material", rule.SecretKey)
	}
	return value, nil
}

func (i *Injector) resolveOAuth2AccessToken(ctx context.Context, rule Rule) (string, error) {
	if token, ok := i.lookupCachedOAuth2Token(rule.OAuth2CacheKey); ok {
		return token, nil
	}

	result := i.oauth2Refresh.DoChan(rule.OAuth2CacheKey, func() (any, error) {
		if token, ok := i.lookupCachedOAuth2Token(rule.OAuth2CacheKey); ok {
			return token, nil
		}
		refreshed, err := i.refreshOAuth2Token(ctx, rule)
		if err != nil {
			return "", err
		}
		i.storeCachedOAuth2Token(rule.OAuth2CacheKey, refreshed)
		i.emitCredentialRefreshSuccess(rule, refreshed.expiresAt)
		return refreshed.accessToken, nil
	})

	select {
	case <-ctx.Done():
		return "", i.oauth2RefreshError(rule, "singleflight wait", ctx.Err())
	case res := <-result:
		if res.Err != nil {
			return "", res.Err
		}
		token, _ := res.Val.(string)
		if strings.TrimSpace(token) == "" {
			return "", i.oauth2RefreshError(rule, "cache lookup", fmt.Errorf("refresh produced no access token"))
		}
		return token, nil
	}
}

func (i *Injector) lookupCachedOAuth2Token(cacheKey string) (string, bool) {
	i.cacheMu.RLock()
	cached, ok := i.oauth2Cache[cacheKey]
	i.cacheMu.RUnlock()
	if !ok {
		return "", false
	}
	if !cached.expiresAt.After(i.now().Add(oauth2ExpiryBuffer)) {
		return "", false
	}
	return cached.accessToken, true
}

func (i *Injector) storeCachedOAuth2Token(cacheKey string, cached oauth2CachedToken) {
	i.cacheMu.Lock()
	defer i.cacheMu.Unlock()
	i.oauth2Cache[cacheKey] = cached
}

func (i *Injector) refreshOAuth2Token(ctx context.Context, rule Rule) (oauth2CachedToken, error) {
	if rule.OAuth2Provider == nil || strings.TrimSpace(rule.OAuth2Provider.TokenURL) == "" {
		return oauth2CachedToken{}, i.oauth2RefreshError(rule, "provider config", fmt.Errorf("token endpoint is required"))
	}

	refreshSecrets, err := i.loadOAuth2RefreshSecrets(ctx, rule)
	if err != nil {
		return oauth2CachedToken{}, err
	}

	refreshClient := *i.refreshClient
	captureTransport := &oauth2CaptureTransport{base: refreshClient.Transport}
	refreshClient.Transport = captureTransport
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, &refreshClient)
	config := &oauth2.Config{
		ClientID:     refreshSecrets.clientID,
		ClientSecret: refreshSecrets.clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   rule.OAuth2Provider.AuthURL,
			TokenURL:  rule.OAuth2Provider.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: append([]string(nil), rule.Scopes...),
	}

	refreshed, err := config.TokenSource(refreshCtx, &oauth2.Token{RefreshToken: refreshSecrets.refreshToken}).Token()
	if err != nil {
		if captureTransport.lastStatus >= 200 && captureTransport.lastStatus < 300 {
			if parseErr := oauth2ResponseParseError(captureTransport.lastBody); parseErr != nil {
				return oauth2CachedToken{}, i.oauth2RefreshError(rule, "response parse", parseErr)
			}
		}
		stage, cause := sanitizeOAuth2TokenSourceError(err)
		return oauth2CachedToken{}, i.oauth2RefreshError(rule, stage, cause)
	}

	accessToken := strings.TrimSpace(refreshed.AccessToken)
	if accessToken == "" {
		return oauth2CachedToken{}, i.oauth2RefreshError(rule, "response parse", fmt.Errorf("access_token is required"))
	}
	expiresIn, err := oauth2ExpiresIn(captureTransport.lastBody)
	if err != nil {
		return oauth2CachedToken{}, i.oauth2RefreshError(rule, "response parse", err)
	}

	return oauth2CachedToken{
		accessToken: accessToken,
		expiresAt:   i.now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

func sanitizeOAuth2TokenSourceError(err error) (stage string, cause error) {
	var retrieveErr *oauth2.RetrieveError
	switch {
	case errors.As(err, &retrieveErr):
		if retrieveErr.Response != nil {
			return "token request", fmt.Errorf("provider returned status %d", retrieveErr.Response.StatusCode)
		}
		return "response parse", fmt.Errorf("oauth2 token retrieval failed")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "token request", err
	default:
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "access_token"):
			return "response parse", fmt.Errorf("access_token is required")
		case strings.Contains(msg, "expires_in"):
			return "response parse", fmt.Errorf("expires_in is required")
		case strings.Contains(msg, "parse") || strings.Contains(msg, "decode") || strings.Contains(msg, "json"):
			return "response parse", fmt.Errorf("oauth2 response parse failed")
		default:
			return "token request", err
		}
	}
}

func (t *oauth2CaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	t.lastStatus = resp.StatusCode
	t.lastBody = append(t.lastBody[:0], body...)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func oauth2ResponseParseError(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var payload oauth2TokenResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("oauth2 response parse failed")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return fmt.Errorf("access_token is required")
	}
	_, err := parseOAuth2ExpiresIn(payload.ExpiresIn)
	return err
}

func oauth2ExpiresIn(body []byte) (int64, error) {
	var payload oauth2TokenResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("oauth2 response parse failed")
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return 0, fmt.Errorf("access_token is required")
	}
	return parseOAuth2ExpiresIn(payload.ExpiresIn)
}

type oauth2RefreshSecrets struct {
	clientID     string
	clientSecret string
	refreshToken string
}

func (i *Injector) loadOAuth2RefreshSecrets(ctx context.Context, rule Rule) (oauth2RefreshSecrets, error) {
	clientID, err := i.requireOAuth2Secret(ctx, rule, "client_id")
	if err != nil {
		return oauth2RefreshSecrets{}, err
	}
	refreshToken, err := i.requireOAuth2Secret(ctx, rule, "refresh_token")
	if err != nil {
		return oauth2RefreshSecrets{}, err
	}
	clientSecret, err := i.optionalOAuth2Secret(ctx, rule, "client_secret")
	if err != nil {
		return oauth2RefreshSecrets{}, err
	}
	return oauth2RefreshSecrets{
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
	}, nil
}

func (i *Injector) requireOAuth2Secret(ctx context.Context, rule Rule, family string) (string, error) {
	key, err := tooldef.CredentialFamilyMemberKey(rule.OAuth2SecretFamily, family)
	if err != nil {
		return "", i.oauth2RefreshError(rule, "secret reread", err)
	}
	secret, err := i.store.Get(ctx, key)
	if err != nil {
		return "", i.oauth2RefreshError(rule, "secret reread", fmt.Errorf("%s: %w", family, err))
	}
	value := strings.TrimSpace(string(secret))
	if value == "" {
		return "", i.oauth2RefreshError(rule, "secret reread", fmt.Errorf("%s resolved unusable secret material", family))
	}
	return value, nil
}

func (i *Injector) optionalOAuth2Secret(ctx context.Context, rule Rule, family string) (string, error) {
	key, err := tooldef.CredentialFamilyMemberKey(rule.OAuth2SecretFamily, family)
	if err != nil {
		return "", i.oauth2RefreshError(rule, "secret reread", err)
	}
	secret, err := i.store.Get(ctx, key)
	if err != nil {
		if err == secrets.ErrNotFound {
			return "", nil
		}
		return "", i.oauth2RefreshError(rule, "secret reread", fmt.Errorf("%s: %w", family, err))
	}
	return strings.TrimSpace(string(secret)), nil
}

func (i *Injector) oauth2RefreshError(rule Rule, stage string, cause error) error {
	i.emitCredentialRefreshFailure(rule, stage, classifyOAuth2RefreshFailure(stage, cause))
	return fmt.Errorf(
		"oauth2 refresh for credential %q (cache %q) failed during %s: %w; re-authorize by updating client_id, client_secret, and refresh_token secrets",
		rule.Name,
		rule.OAuth2CacheKey,
		stage,
		cause,
	)
}

func classifyOAuth2RefreshFailure(stage string, cause error) string {
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(cause, context.Canceled):
		return "canceled"
	}

	switch stage {
	case "provider config":
		return "provider_config"
	case "response parse":
		return "malformed_response"
	case "cache lookup":
		return "empty_access_token"
	case "singleflight wait":
		return "wait_failed"
	case "secret reread":
		if errors.Is(cause, secrets.ErrNotFound) {
			return "missing_secret_material"
		}
		if strings.Contains(cause.Error(), "resolved unusable secret material") {
			return "invalid_secret_material"
		}
		return "secret_read_failed"
	case "token request":
		if strings.Contains(cause.Error(), "provider returned status") {
			return "provider_error"
		}
		return "request_failed"
	default:
		return "unknown"
	}
}

func parseOAuth2ExpiresIn(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, fmt.Errorf("expires_in is required")
	}

	var numeric json.Number
	if err := json.Unmarshal(raw, &numeric); err == nil {
		value, err := numeric.Int64()
		if err != nil {
			return 0, fmt.Errorf("expires_in must be an integer")
		}
		if value <= 0 {
			return 0, fmt.Errorf("expires_in must be positive")
		}
		return value, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		value, err := json.Number(strings.TrimSpace(asString)).Int64()
		if err != nil {
			return 0, fmt.Errorf("expires_in must be numeric")
		}
		if value <= 0 {
			return 0, fmt.Errorf("expires_in must be positive")
		}
		return value, nil
	}

	return 0, fmt.Errorf("expires_in must be numeric")
}

func (i *Injector) injectBearerHeader(rawURL string, headers *fetch.Headers, rule Rule, value string) (string, error) {
	if hasHeader(headers, "authorization") {
		return rawURL, nil
	}
	if headers == nil {
		return rawURL, fmt.Errorf("inject authorization header for %q: headers are unavailable", rule.Name)
	}
	if err := headers.Append("Authorization", "Bearer "+value); err != nil {
		return rawURL, fmt.Errorf("inject authorization header for %q: %w", rule.Name, err)
	}
	i.emitCredentialInjected(rule, rawURL)
	return rawURL, nil
}

func (i *Injector) injectBasicAuth(rawURL string, headers *fetch.Headers, rule Rule, value string) (string, error) {
	if hasHeader(headers, "authorization") {
		return rawURL, nil
	}
	if headers == nil {
		return rawURL, fmt.Errorf("inject authorization header for %q: headers are unavailable", rule.Name)
	}
	username, password, err := parseBasicAuthSecret(value)
	if err != nil {
		return rawURL, fmt.Errorf("transport credential %q resolved malformed basic_auth secret material", rule.SecretKey)
	}
	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	if err := headers.Append("Authorization", authorization); err != nil {
		return rawURL, fmt.Errorf("inject authorization header for %q: %w", rule.Name, err)
	}
	i.emitCredentialInjected(rule, rawURL)
	return rawURL, nil
}

func (i *Injector) injectAPIKeyHeader(rawURL string, headers *fetch.Headers, rule Rule, value string) (string, error) {
	if hasHeader(headers, rule.Inject.HeaderName) {
		return rawURL, nil
	}
	if headers == nil {
		return rawURL, fmt.Errorf("inject api key header for %q: headers are unavailable", rule.Name)
	}
	if err := headers.Append(rule.Inject.HeaderName, value); err != nil {
		return rawURL, fmt.Errorf("inject api key header for %q: %w", rule.Name, err)
	}
	i.emitCredentialInjected(rule, rawURL)
	return rawURL, nil
}

func (i *Injector) injectAPIKeyQuery(parsed *url.URL, rule Rule, value string) (string, error) {
	query := parsed.Query()
	if query.Get(rule.Inject.QueryName) == "" {
		query.Set(rule.Inject.QueryName, value)
		parsed.RawQuery = query.Encode()
		rewritten := parsed.String()
		i.emitCredentialInjected(rule, rewritten)
		return rewritten, nil
	}
	return parsed.String(), nil
}

func parseBasicAuthSecret(secret string) (string, string, error) {
	username, password, ok := strings.Cut(secret, ":")
	if !ok || username == "" || password == "" {
		return "", "", fmt.Errorf("basic auth secret must be username:password")
	}
	return username, password, nil
}

func (i *Injector) emitCredentialInjected(rule Rule, rawURL string) {
	if i == nil {
		return
	}
	host, err := auditHost(rawURL)
	if err != nil {
		return
	}
	event, err := audit.NewCredentialInjected(host, rule.Name, rule.Inject.Method)
	if err != nil {
		return
	}
	audit.Emit(i.auditSink, event)
}

func (i *Injector) emitCredentialRefreshSuccess(rule Rule, expiresAt time.Time) {
	if i == nil {
		return
	}
	event, err := audit.NewCredentialRefreshSuccess(rule.Name, rule.OAuth2CacheKey, expiresAt)
	if err != nil {
		return
	}
	audit.Emit(i.auditSink, event)
}

func (i *Injector) emitCredentialRefreshFailure(rule Rule, stage, reason string) {
	if i == nil {
		return
	}
	event, err := audit.NewCredentialRefreshFailure(rule.Name, rule.OAuth2CacheKey, stage, reason)
	if err != nil {
		return
	}
	audit.Emit(i.auditSink, event)
}

func auditHost(rawURL string) (string, error) {
	parsed, err := parseRequestURL(rawURL)
	if err != nil {
		return "", err
	}
	return strings.ToLower(parsed.Hostname()), nil
}

// NormalizeAllowedHosts canonicalizes a resolved allowlist into a deterministic,
// deduplicated runtime form.
func NormalizeAllowedHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(hosts))
	resolved := make([]string, 0, len(hosts))
	for _, host := range hosts {
		normalized, err := normalizeHostPattern(host)
		if err != nil || normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		resolved = append(resolved, normalized)
	}
	if len(resolved) == 0 {
		return nil
	}
	sort.Strings(resolved)
	return resolved
}

func normalizeRule(rule Rule) (Rule, error) {
	if len(rule.Inject.Hosts) == 0 {
		return Rule{}, fmt.Errorf("credential %q must declare at least one inject host", rule.Name)
	}

	pathPrefix, err := normalizePathPrefix(rule.Inject.PathPrefix)
	if err != nil {
		return Rule{}, fmt.Errorf("credential %q: %w", rule.Name, err)
	}

	method := strings.TrimSpace(rule.Inject.Method)
	if method == "" {
		method = "bearer_header"
	}
	if !supportedMethod(method) {
		return Rule{}, fmt.Errorf("credential %q uses unsupported injection method %q", rule.Name, rule.Inject.Method)
	}

	headerName, queryName, err := normalizeInjectConfig(method, rule.Inject)
	if err != nil {
		return Rule{}, fmt.Errorf("credential %q: %w", rule.Name, err)
	}

	hosts := make([]string, 0, len(rule.Inject.Hosts))
	matchers := make([]hostMatcher, 0, len(rule.Inject.Hosts))
	for idx, rawHost := range rule.Inject.Hosts {
		normalizedHost, err := normalizeHostPattern(rawHost)
		if err != nil {
			return Rule{}, fmt.Errorf("credential %q inject host[%d]: %w", rule.Name, idx, err)
		}
		hosts = append(hosts, normalizedHost)
		matchers = append(matchers, newHostMatcher(normalizedHost))
	}
	sort.Strings(hosts)
	sort.Slice(matchers, func(i, j int) bool {
		return matchers[i].pattern < matchers[j].pattern
	})

	rule.Inject.Hosts = hosts
	rule.Inject.Method = method
	rule.Inject.PathPrefix = pathPrefix
	rule.Inject.HeaderName = headerName
	rule.Inject.QueryName = queryName
	rule.pathPrefix = pathPrefix
	rule.hostMatchers = matchers
	rule.Scopes = append([]string(nil), rule.Scopes...)
	return rule, nil
}

func rejectAmbiguousRules(rules []Rule) error {
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			if rulesOverlapAtSameSpecificity(rules[i], rules[j]) {
				return fmt.Errorf(
					"ambiguous transport credential rules: %q and %q both resolve to hosts %v with path prefix %q",
					rules[i].Name,
					rules[j].Name,
					sharedPatterns(rules[i], rules[j]),
					sharedPathPrefix(rules[i].pathPrefix, rules[j].pathPrefix),
				)
			}
		}
	}
	return nil
}

func rulesOverlapAtSameSpecificity(left, right Rule) bool {
	if left.pathPrefix != right.pathPrefix {
		return false
	}
	for _, lm := range left.hostMatchers {
		for _, rm := range right.hostMatchers {
			if lm.pattern == rm.pattern {
				return true
			}
		}
	}
	return false
}

func sharedPatterns(left, right Rule) []string {
	set := make([]string, 0)
	for _, lm := range left.hostMatchers {
		for _, rm := range right.hostMatchers {
			if lm.pattern == rm.pattern {
				set = append(set, lm.pattern)
			}
		}
	}
	sort.Strings(set)
	return set
}

func sharedPathPrefix(left, right string) string {
	if left != "" {
		return left
	}
	return right
}

func parseRequestURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse request url: %w", err)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("parse request url: missing host")
	}
	return parsed, nil
}

func (i *Injector) matchRule(parsed *url.URL) (Rule, bool, error) {
	host := strings.ToLower(parsed.Hostname())
	bestScore := matchScore{hostScore: -1, pathScore: -1}
	var best Rule
	matched := false
	for _, rule := range i.rules {
		score, ok := rule.match(host, parsed)
		if !ok {
			continue
		}
		if !matched || score.moreSpecificThan(bestScore) {
			bestScore = score
			best = rule
			matched = true
			continue
		}
		if score.equal(bestScore) {
			return Rule{}, false, fmt.Errorf("ambiguous transport credentials for %s", parsed.String())
		}
	}
	if !matched {
		return Rule{}, false, nil
	}
	return best, true, nil
}

func (r Rule) match(host string, parsed *url.URL) (matchScore, bool) {
	bestHostScore := -1
	for _, matcher := range r.hostMatchers {
		score, ok := matcher.match(host)
		if ok && score > bestHostScore {
			bestHostScore = score
		}
	}
	if bestHostScore < 0 {
		return matchScore{}, false
	}

	if r.pathPrefix != "" {
		escapedPath := parsed.EscapedPath()
		if escapedPath == "" {
			escapedPath = "/"
		}
		if !strings.HasPrefix(escapedPath, r.pathPrefix) && !strings.HasPrefix(parsed.Path, r.pathPrefix) {
			return matchScore{}, false
		}
	}

	return matchScore{hostScore: bestHostScore, pathScore: len(r.pathPrefix)}, true
}

func (s matchScore) moreSpecificThan(other matchScore) bool {
	if s.hostScore != other.hostScore {
		return s.hostScore > other.hostScore
	}
	return s.pathScore > other.pathScore
}

func (s matchScore) equal(other matchScore) bool {
	return s.hostScore == other.hostScore && s.pathScore == other.pathScore
}

func newHostMatcher(pattern string) hostMatcher {
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		return hostMatcher{pattern: pattern, suffix: suffix}
	}
	return hostMatcher{pattern: pattern, suffix: pattern, exact: true}
}

func (m hostMatcher) match(host string) (int, bool) {
	if m.exact {
		if host == m.pattern {
			return exactHostBonus + len(m.pattern), true
		}
		return 0, false
	}
	if m.suffix == "" || !strings.HasSuffix(host, "."+m.suffix) {
		return 0, false
	}
	return len(m.suffix), true
}

func normalizeHostPattern(pattern string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(pattern))
	if trimmed == "" {
		return "", fmt.Errorf("host pattern must not be empty")
	}
	if strings.Contains(trimmed, "/") {
		return "", fmt.Errorf("host pattern %q must not include a path", pattern)
	}
	if strings.HasPrefix(trimmed, "*.") {
		suffix := strings.TrimPrefix(trimmed, "*.")
		if suffix == "" || strings.Contains(suffix, "*") {
			return "", fmt.Errorf("host pattern %q is not a valid wildcard", pattern)
		}
		return "*." + suffix, nil
	}
	if strings.Contains(trimmed, "*") {
		return "", fmt.Errorf("host pattern %q is not a valid exact host", pattern)
	}
	return trimmed, nil
}

func normalizePathPrefix(pathPrefix string) (string, error) {
	if pathPrefix == "" {
		return "", nil
	}
	trimmed := strings.TrimSpace(pathPrefix)
	if trimmed == "" {
		return "", fmt.Errorf("path prefix must not be empty")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("path prefix %q must start with '/'", pathPrefix)
	}
	cleaned := pathpkg.Clean(trimmed)
	if cleaned == "." {
		cleaned = "/"
	}
	if cleaned != "/" {
		cleaned = strings.TrimRight(cleaned, "/")
	}
	return cleaned, nil
}

func normalizeInjectConfig(method string, inject tooldef.CredentialInject) (headerName string, queryName string, err error) {
	headerName = strings.TrimSpace(inject.HeaderName)
	queryName = strings.TrimSpace(inject.QueryName)

	switch method {
	case "bearer_header", "basic_auth":
		if headerName != "" {
			return "", "", fmt.Errorf("inject.headerName is only allowed for api_key_header")
		}
		if queryName != "" {
			return "", "", fmt.Errorf("inject.queryName is only allowed for api_key_query")
		}
	case "api_key_header":
		if headerName == "" {
			return "", "", fmt.Errorf("inject.headerName must not be empty when method is api_key_header")
		}
		if !validHeaderName(headerName) {
			return "", "", fmt.Errorf("inject.headerName %q is invalid", inject.HeaderName)
		}
		if queryName != "" {
			return "", "", fmt.Errorf("inject.queryName is only allowed for api_key_query")
		}
	case "api_key_query":
		if queryName == "" {
			return "", "", fmt.Errorf("inject.queryName must not be empty when method is api_key_query")
		}
		if !validQueryName(queryName) {
			return "", "", fmt.Errorf("inject.queryName %q is invalid", inject.QueryName)
		}
		if headerName != "" {
			return "", "", fmt.Errorf("inject.headerName is only allowed for api_key_header")
		}
	}

	return headerName, queryName, nil
}

func supportedMethod(method string) bool {
	switch method {
	case "bearer_header", "basic_auth", "api_key_header", "api_key_query":
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c > 127 {
			return false
		}
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' ||
			c == '\'' || c == '*' || c == '+' || c == '-' || c == '.' ||
			c == '^' || c == '_' || c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

func validQueryName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch r {
		case '&', '=', '#', '?':
			return false
		}
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hasHeader(headers *fetch.Headers, name string) bool {
	if headers == nil {
		return false
	}
	name = strings.ToLower(name)
	for _, entry := range headers.Entries() {
		if entry[0] == name {
			return true
		}
	}
	return false
}
