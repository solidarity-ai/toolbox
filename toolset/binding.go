package toolset

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// Binding describes how a parameter is resolved at call time.
type Binding struct {
	Value  string // CEL expression: "context.customer_id", "params.channel", "'literal'"
	Hidden bool   // If true, agent never sees this param
	Check  string // Optional CEL guard: "params.channel in context.allowed_channels"
}

// BoundTool associates a tool reference with per-param bindings.
type BoundTool struct {
	ToolRef  string             // e.g. "calc.add"
	Bindings map[string]Binding // param name -> binding
}

// ResolvedCredential is the runtime-only credential surface derived from a
// package's declarative credential metadata. It is not visible to tools.
type ResolvedCredential struct {
	Name            string
	Type            tooldef.CredentialType
	Provider        string
	Scopes          []string
	SecretNamespace string
	Inject          tooldef.CredentialInject
}

// SecretKey returns the package-scoped secret key for direct credential lookup.
func (c ResolvedCredential) SecretKey() string {
	if c.SecretNamespace == "" {
		return c.Name
	}
	if c.Name == "" {
		return c.SecretNamespace
	}
	return c.SecretNamespace + "/" + c.Name
}

// matches reports whether this credential should be injected for the request URL.
func (c ResolvedCredential) matches(rawURL string) (bool, int, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, 0, fmt.Errorf("parse request url: %w", err)
	}
	if parsed.Hostname() == "" {
		return false, 0, nil
	}
	pathScore := 0
	if c.Inject.PathPrefix != "" {
		if !strings.HasPrefix(parsed.EscapedPath(), c.Inject.PathPrefix) && !strings.HasPrefix(parsed.Path, c.Inject.PathPrefix) {
			return false, 0, nil
		}
		pathScore = len(c.Inject.PathPrefix)
	}

	host := strings.ToLower(parsed.Hostname())
	bestHostScore := -1
	for _, pattern := range c.Inject.Hosts {
		score, ok := matchCredentialHost(host, pattern)
		if ok && score > bestHostScore {
			bestHostScore = score
		}
	}
	if bestHostScore < 0 {
		return false, 0, nil
	}
	return true, bestHostScore*10000 + pathScore, nil
}

// ResolvedAuth is the runtime-only auth context for one resolved tool.
type ResolvedAuth struct {
	Store       secrets.SecretStore
	Credentials []ResolvedCredential
}

// InjectRequest applies transport-managed auth to a request represented as a
// URL plus Fetch-style headers. It mutates headers and may return a rewritten URL.
func (a ResolvedAuth) InjectRequest(ctx context.Context, rawURL string, headers *fetch.Headers) (string, error) {
	if len(a.Credentials) == 0 {
		return rawURL, nil
	}

	credential, ok, err := a.matchingCredential(rawURL)
	if err != nil {
		return rawURL, err
	}
	if !ok {
		return rawURL, nil
	}
	if a.Store == nil {
		return rawURL, fmt.Errorf("transport auth configured for %q but no secret store is available", credential.SecretKey())
	}

	secret, err := a.Store.Get(ctx, credential.SecretKey())
	if err != nil {
		return rawURL, fmt.Errorf("resolve transport credential %q: %w", credential.SecretKey(), err)
	}
	value := string(secret)

	switch credential.Inject.Method {
	case "", "bearer_header":
		if !hasHeader(headers, "authorization") {
			if err := headers.Append("Authorization", "Bearer "+value); err != nil {
				return rawURL, fmt.Errorf("inject authorization header for %q: %w", credential.Name, err)
			}
		}
		return rawURL, nil
	case "api_key_header":
		if !hasHeader(headers, "x-api-key") {
			if err := headers.Append("X-API-Key", value); err != nil {
				return rawURL, fmt.Errorf("inject api key header for %q: %w", credential.Name, err)
			}
		}
		return rawURL, nil
	case "api_key_query":
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return rawURL, fmt.Errorf("parse request url: %w", err)
		}
		query := parsed.Query()
		if query.Get("key") == "" {
			query.Set("key", value)
			parsed.RawQuery = query.Encode()
		}
		return parsed.String(), nil
	default:
		return rawURL, fmt.Errorf("credential %q uses unsupported injection method %q", credential.Name, credential.Inject.Method)
	}
}

func (a ResolvedAuth) matchingCredential(rawURL string) (ResolvedCredential, bool, error) {
	bestScore := -1
	var best ResolvedCredential
	for _, credential := range a.Credentials {
		matched, score, err := credential.matches(rawURL)
		if err != nil {
			return ResolvedCredential{}, false, err
		}
		if !matched {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = credential
			continue
		}
		if score == bestScore {
			return ResolvedCredential{}, false, fmt.Errorf("ambiguous transport credentials for %s", rawURL)
		}
	}
	if bestScore < 0 {
		return ResolvedCredential{}, false, nil
	}
	return best, true, nil
}

func matchCredentialHost(host string, pattern string) (int, bool) {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return 0, false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		if suffix == "" || !strings.HasSuffix(host, "."+suffix) {
			return 0, false
		}
		return len(suffix), true
	}
	if host == pattern {
		return 1000 + len(pattern), true
	}
	return 0, false
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

// Config is the input to Resolve(). It carries bindings, context, and resolved
// runtime dependencies such as transport-managed secret state.
type Config struct {
	Tools            []BoundTool        // Explicitly bound tools
	ResourceBindings map[string]Binding // Resource-level bindings by canonical name
	Context          map[string]any     // Flat key-value context from harness
	SecretStore      secrets.SecretStore
}
