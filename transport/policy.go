package transport

import (
	"context"

	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
)

// Policy is the transport-owned runtime policy surface for one resolved tool.
// It keeps request mutation and allowlist state on the transport boundary so
// runtime consumers do not need to understand toolset-specific auth plumbing.
type Policy struct {
	injector     *Injector
	allowedHosts []string
}

// NewPolicy assembles the shared runtime transport policy for one tool. The
// policy may carry credential injection rules, allowed-host metadata, or both.
func NewPolicy(store secrets.SecretStore, rules []Rule, allowedHosts []string) (*Policy, error) {
	injector, err := NewInjector(store, rules)
	if err != nil {
		return nil, err
	}

	normalizedAllowedHosts := NormalizeAllowedHosts(allowedHosts)
	if (injector == nil || len(injector.Rules()) == 0) && len(normalizedAllowedHosts) == 0 {
		return nil, nil
	}

	return &Policy{
		injector:     injector,
		allowedHosts: normalizedAllowedHosts,
	}, nil
}

// PrepareRequest applies transport-managed request mutation. Allowlist
// enforcement is intentionally deferred; this seam exists so both fetch and the
// MITM proxy can share one resolved runtime policy object.
func (p *Policy) PrepareRequest(ctx context.Context, rawURL string, headers *fetch.Headers) (string, error) {
	if p == nil || p.injector == nil {
		return rawURL, nil
	}
	return p.injector.InjectRequest(ctx, rawURL, headers)
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
