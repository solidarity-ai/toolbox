package transport

import (
	"context"
	"fmt"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const exactHostBonus = 1000

// Rule is the transport-owned runtime rule for simple credential injection.
// It contains only runtime metadata needed for matching and request mutation.
type Rule struct {
	Name      string
	SecretKey string
	Type      tooldef.CredentialType
	Provider  string
	Scopes    []string
	Inject    tooldef.CredentialInject

	hostMatchers []hostMatcher
	pathPrefix   string
}

// Injector validates, matches, and injects simple transport credentials.
type Injector struct {
	store secrets.SecretStore
	rules []Rule
}

type hostMatcher struct {
	pattern string
	suffix  string
	exact   bool
}

type matchScore struct {
	hostScore int
	pathScore int
}

// NewInjector canonicalizes and validates the provided rules up front so live
// requests only evaluate deterministic match state.
func NewInjector(store secrets.SecretStore, rules []Rule) (*Injector, error) {
	if len(rules) == 0 {
		return &Injector{store: store}, nil
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

	return &Injector{store: store, rules: normalized}, nil
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

	secret, err := i.store.Get(ctx, rule.SecretKey)
	if err != nil {
		return rawURL, fmt.Errorf("resolve transport credential %q: %w", rule.SecretKey, err)
	}
	value := string(secret)
	if strings.TrimSpace(value) == "" {
		return rawURL, fmt.Errorf("transport credential %q resolved unusable secret material", rule.SecretKey)
	}

	switch rule.Inject.Method {
	case "bearer_header":
		if !hasHeader(headers, "authorization") {
			if err := headers.Append("Authorization", "Bearer "+value); err != nil {
				return rawURL, fmt.Errorf("inject authorization header for %q: %w", rule.Name, err)
			}
		}
		return rawURL, nil
	case "api_key_header":
		if !hasHeader(headers, "x-api-key") {
			if err := headers.Append("X-API-Key", value); err != nil {
				return rawURL, fmt.Errorf("inject api key header for %q: %w", rule.Name, err)
			}
		}
		return rawURL, nil
	case "api_key_query":
		query := parsed.Query()
		if query.Get("key") == "" {
			query.Set("key", value)
			parsed.RawQuery = query.Encode()
		}
		return parsed.String(), nil
	default:
		return rawURL, fmt.Errorf("credential %q uses unsupported injection method %q", rule.Name, rule.Inject.Method)
	}
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

func supportedMethod(method string) bool {
	switch method {
	case "bearer_header", "api_key_header", "api_key_query":
		return true
	default:
		return false
	}
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
