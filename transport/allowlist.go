package transport

// HostAllowlist restricts which hosts outbound requests may contact.
// An empty allowlist denies all requests.
type HostAllowlist struct {
	patterns []string
}

// NewHostAllowlist creates an allowlist from host patterns.
// Supports exact match ("api.slack.com") and wildcard prefix ("*.googleapis.com").
func NewHostAllowlist(patterns []string) *HostAllowlist {
	return &HostAllowlist{patterns: patterns}
}

// Allows returns true if the given host is permitted.
func (a *HostAllowlist) Allows(host string) bool {
	for _, p := range a.patterns {
		if hostMatches(p, host) {
			return true
		}
	}
	return false
}
