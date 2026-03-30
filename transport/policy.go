package transport

import (
	"context"
	"fmt"
	"strings"

	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
)

// Policy is the transport-owned runtime policy surface for one resolved tool.
// It keeps request mutation and allowlist state on the transport boundary so
// runtime consumers do not need to understand toolset-specific auth plumbing.
//
// A policy may exist even when both rules and the normalized allowlist are
// empty. That runtime-only shape intentionally represents deny-by-default for
// transport-aware runtimes without exposing an allow-any state through callers
// that use AllowedHosts().
type Policy struct {
	injector     *Injector
	allowedHosts []string
}

// NewPolicy assembles the shared runtime transport policy for one tool. The
// policy may carry credential injection rules, allowed-host metadata, or both.
// When requireRuntimePolicy is true, an otherwise empty policy is still
// materialized so runtime preflight can distinguish "deny by default" from
// "no transport policy was built".
func NewPolicy(store secrets.SecretStore, rules []Rule, allowedHosts []string, requireRuntimePolicy bool) (*Policy, error) {
	injector, err := NewInjector(store, rules)
	if err != nil {
		return nil, err
	}

	normalizedAllowedHosts := NormalizeAllowedHosts(allowedHosts)
	if !requireRuntimePolicy && (injector == nil || len(injector.Rules()) == 0) && len(normalizedAllowedHosts) == 0 {
		return nil, nil
	}

	return &Policy{
		injector:     injector,
		allowedHosts: normalizedAllowedHosts,
	}, nil
}

// PrepareRequest applies transport-managed request mutation and request-host
// allowlist enforcement for the shared runtime policy seam. Injector mutation
// intentionally runs first so transport-owned auth preparation failures surface
// before allowlist evaluation, but preflight failures always return the caller's
// original request URL so hidden mutations do not leak back across the seam.
func (p *Policy) PrepareRequest(ctx context.Context, rawURL string, headers *fetch.Headers) (string, error) {
	if p == nil {
		return rawURL, nil
	}

	preparedURL := rawURL
	if p.injector != nil {
		var err error
		preparedURL, err = p.injector.InjectRequest(ctx, rawURL, headers)
		if err != nil {
			return rawURL, err
		}
	}

	if err := p.enforceAllowedHost(preparedURL); err != nil {
		return rawURL, err
	}
	return preparedURL, nil
}

func (p *Policy) enforceAllowedHost(rawURL string) error {
	parsed, err := parseRequestURL(rawURL)
	if err != nil {
		return err
	}

	host := strings.ToLower(parsed.Hostname())
	if len(p.allowedHosts) == 0 {
		return fmt.Errorf("transport denied request to host %q: no allowed hosts declared", host)
	}

	for _, allowedHost := range p.allowedHosts {
		if _, ok := newHostMatcher(allowedHost).match(host); ok {
			return nil
		}
	}

	return fmt.Errorf("transport denied request to host %q: not allowed by policy", host)
}

// Rules returns the normalized credential rules carried by this policy.
func (p *Policy) Rules() []Rule {
	if p == nil || p.injector == nil {
		return nil
	}
	return p.injector.Rules()
}

// AllowedHosts returns the normalized runtime allowlist metadata carried by
// this policy.
func (p *Policy) AllowedHosts() []string {
	if p == nil || len(p.allowedHosts) == 0 {
		return nil
	}
	out := make([]string, len(p.allowedHosts))
	copy(out, p.allowedHosts)
	return out
}
