package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/solidarity-ai/toolbox/internal/buildinfo"
	"github.com/solidarity-ai/toolbox/safeguard"
)

const (
	securityPolicyEnv     = "TOOLBOX_SECURITY_POLICY"
	securityPolicyAPIPath = "/v1/security/policy"
	securityPolicyTimeout = 10 * time.Second
)

func newSecurityPolicyClient() (*safeguard.Client, bool, error) {
	policyURL, enabled, err := securityPolicyConfigFromEnv()
	if err != nil || !enabled {
		return nil, enabled, err
	}
	client, err := safeguard.NewClient(policyURL, &http.Client{Timeout: securityPolicyTimeout}, "")
	if err != nil {
		return nil, false, err
	}
	return client, true, nil
}

func securityPolicyConfigFromEnv() (policyURL string, enabled bool, err error) {
	raw := strings.TrimSpace(os.Getenv(securityPolicyEnv))
	if strings.EqualFold(raw, "off") {
		return "", false, nil
	}
	if raw != "" {
		normalized, err := normalizeSecurityPolicyURL(raw)
		if err != nil {
			return "", false, err
		}
		parsed, _ := url.Parse(normalized)
		if parsed.Path == "" || parsed.Path == "/" {
			normalized = strings.TrimRight(normalized, "/") + securityPolicyAPIPath
		}
		return normalized, true, nil
	}

	registryBaseURL, registryEnabled, err := toolRegistryConfigFromEnv()
	if err != nil || !registryEnabled {
		return "", registryEnabled, err
	}
	return registryBaseURL + securityPolicyAPIPath, true, nil
}

func normalizeSecurityPolicyURL(raw string) (string, error) {
	original := raw
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid %s %q: %w", securityPolicyEnv, original, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid %s %q: unsupported scheme %q", securityPolicyEnv, original, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid %s %q: expected host or absolute URL", securityPolicyEnv, original)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func checkCurrentToolboxPolicy(stderr io.Writer) error {
	version, released := buildinfo.ReleasedBinary()
	if !released {
		return nil
	}
	client, enabled, err := newSecurityPolicyClient()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if err := client.Refresh(context.Background()); err != nil && stderr != nil {
		fallback := "no cached policy is available"
		if client.HasPolicy() {
			fallback = "the last valid cached policy remains active"
		}
		fmt.Fprintf(stderr, "warning: security policy refresh failed; %s: %v\n", fallback, err)
	}
	if err := client.CheckToolbox(context.Background(), version); err != nil {
		return fmt.Errorf("%w; upgrade Toolbox before continuing (for npm installs: npm install -g @include-tools/toolbox@latest)", err)
	}
	return nil
}

func securityCheckExempt(command string) bool {
	return commandMatches(command, "version") ||
		commandMatches(command, "daemon stop") ||
		commandMatches(command, "_daemon stop")
}

func commandMatches(command, prefix string) bool {
	return command == prefix || strings.HasPrefix(command, prefix+" ")
}
