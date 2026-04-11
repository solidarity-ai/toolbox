package registry

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func normalizeToolRegistryClientConfig(baseURL string, client *http.Client) (string, *http.Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", nil, fmt.Errorf("tool registry base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse tool registry base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", nil, fmt.Errorf("tool registry base URL %q must be an absolute http or https URL", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", nil, fmt.Errorf("tool registry base URL %q must use http or https", baseURL)
	}
	if client == nil {
		client = http.DefaultClient
	}

	return strings.TrimRight(parsed.String(), "/"), client, nil
}
