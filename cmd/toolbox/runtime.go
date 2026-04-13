package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	defaultToolsetFilename     = "toolbox.toolset.json"
	defaultToolRegistryBaseURL = "https://packages.include.tools"
	defaultToolRegistryTimeout = 10 * time.Second
)

func newCredentialPolicySource() toolset.PackageCredentialPolicySource {
	return newCredentialRepository()
}

func newCredentialRepository() *credentialrepo.Repository {
	return credentialrepo.New(secrets.NewLocalSecretStore("", ""))
}

func newResolver() (*registry.Resolver, error) {
	cache, err := registry.NewCache("")
	if err != nil {
		return nil, err
	}
	githubBaseURL := os.Getenv("GITHUB_BASE_URL")
	gitURLPrefix := os.Getenv("TOOLBOX_GIT_URL_PREFIX")
	githubClient := newGitHubHTTPClient(os.Getenv("GITHUB_TOKEN"))

	sources := make([]registry.PackageSource, 0, 3)
	registryBaseURL, enabled, err := toolRegistryConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if enabled {
		source, err := registry.NewToolRegistrySource(registryBaseURL, newToolRegistryHTTPClient())
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	sources = append(
		sources,
		registry.NewGitHubReleaseSource(githubBaseURL, githubClient),
		&registry.GitSourceFallback{URLPrefix: gitURLPrefix},
	)

	return registry.NewResolver(cache, sources...), nil
}

func newGitHubHTTPClient(token string) *http.Client {
	if strings.TrimSpace(token) == "" {
		return http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: authTransport{base: transport, token: token}}
}

func newToolRegistryHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultToolRegistryTimeout}
}

func toolRegistryConfigFromEnv() (baseURL string, enabled bool, err error) {
	raw := strings.TrimSpace(os.Getenv("TOOLBOX_REGISTRY"))
	if raw == "" {
		raw = defaultToolRegistryBaseURL
	}
	if strings.EqualFold(raw, "off") {
		return "", false, nil
	}

	normalized, err := normalizeToolRegistryBaseURL(raw)
	if err != nil {
		return "", false, err
	}
	return normalized, true, nil
}

func normalizeToolRegistryBaseURL(raw string) (string, error) {
	original := raw
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: value must not be empty", original)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: %w", original, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: unsupported scheme %q", original, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid TOOLBOX_REGISTRY %q: expected host or absolute URL", original)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header.Get("Authorization") == "" {
		clone.Header.Set("Authorization", "token "+t.token)
	}
	return t.base.RoundTrip(clone)
}
