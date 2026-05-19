package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/registry"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	defaultToolsetFilename     = "toolbox.toolset.json"
	defaultToolRegistryBaseURL = "https://packages.include.tools"
	defaultToolRegistryTimeout = 10 * time.Second
)

type secretStoreOptions struct {
	NoDaemon         bool
	SecretKey        string
	BackupCodeWriter io.Writer
}

func newCredentialPolicySource(opts secretStoreOptions) toolset.PackageCredentialPolicySource {
	return newCredentialRepository(opts)
}

func newCredentialRepository(opts secretStoreOptions) *credentialrepo.Repository {
	return credentialrepo.New(newSecretStore(opts))
}

func newLocalSecretStore(opts secretStoreOptions) *secrets.LocalSecretStore {
	local := secrets.NewLocalSecretStoreWithKey("", "", opts.SecretKey)
	local.SetBackupCodeWriter(opts.BackupCodeWriter)
	return local
}

func newSecretStore(opts secretStoreOptions) secrets.SecretStore {
	local := newLocalSecretStore(opts)
	if opts.NoDaemon {
		return local
	}
	return &daemonPreferredSecretStore{
		primary:  daemon.NewSecretStoreWithBackupCodeWriter(opts.SecretKey, opts.BackupCodeWriter),
		fallback: local,
	}
}

func newManagedSecretStore(opts secretStoreOptions) secrets.ManagedSecretStore {
	if opts.NoDaemon {
		return newLocalSecretStore(opts)
	}
	return &daemonPreferredSecretStore{
		primary:  daemon.NewSecretStoreWithBackupCodeWriter(opts.SecretKey, opts.BackupCodeWriter),
		fallback: newLocalSecretStore(opts),
	}
}

func newLifecycleSecretStore(opts secretStoreOptions) secrets.LifecycleSecretStore {
	if opts.NoDaemon {
		return newLocalSecretStore(opts)
	}
	return &daemonPreferredSecretStore{
		primary:  daemon.NewSecretStoreWithBackupCodeWriter(opts.SecretKey, opts.BackupCodeWriter),
		fallback: newLocalSecretStore(opts),
	}
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

type daemonPreferredSecretStore struct {
	primary  secrets.LifecycleSecretStore
	fallback secrets.LifecycleSecretStore

	mu          sync.RWMutex
	useFallback bool
}

var _ secrets.LifecycleSecretStore = (*daemonPreferredSecretStore)(nil)

func (s *daemonPreferredSecretStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s.shouldUseFallback() {
		return s.fallback.Get(ctx, key)
	}
	value, err := s.primary.Get(ctx, key)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Get(ctx, key)
	}
	return value, err
}

func (s *daemonPreferredSecretStore) Set(ctx context.Context, key string, value []byte) error {
	if s.shouldUseFallback() {
		return s.fallback.Set(ctx, key, value)
	}
	err := s.primary.Set(ctx, key, value)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Set(ctx, key, value)
	}
	return err
}

func (s *daemonPreferredSecretStore) Delete(ctx context.Context, key string) error {
	if s.shouldUseFallback() {
		return s.fallback.Delete(ctx, key)
	}
	err := s.primary.Delete(ctx, key)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Delete(ctx, key)
	}
	return err
}

func (s *daemonPreferredSecretStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.shouldUseFallback() {
		return s.fallback.List(ctx, prefix)
	}
	keys, err := s.primary.List(ctx, prefix)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.List(ctx, prefix)
	}
	return keys, err
}

func (s *daemonPreferredSecretStore) Unlock(ctx context.Context, unlockKey string) error {
	if s.shouldUseFallback() {
		return s.fallback.Unlock(ctx, unlockKey)
	}
	err := s.primary.Unlock(ctx, unlockKey)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Unlock(ctx, unlockKey)
	}
	return err
}

func (s *daemonPreferredSecretStore) Lock(ctx context.Context) error {
	if s.shouldUseFallback() {
		return s.fallback.Lock(ctx)
	}
	err := s.primary.Lock(ctx)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Lock(ctx)
	}
	return err
}

func (s *daemonPreferredSecretStore) Initialized() (bool, error) {
	if s.shouldUseFallback() {
		return s.fallback.Initialized()
	}
	initialized, err := s.primary.Initialized()
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Initialized()
	}
	return initialized, err
}

func (s *daemonPreferredSecretStore) Setup(ctx context.Context, unlockKey string) ([]string, error) {
	if s.shouldUseFallback() {
		return s.fallback.Setup(ctx, unlockKey)
	}
	codes, err := s.primary.Setup(ctx, unlockKey)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.Setup(ctx, unlockKey)
	}
	return codes, err
}

func (s *daemonPreferredSecretStore) BackupCodes(ctx context.Context, unlockKey string) ([]string, error) {
	if s.shouldUseFallback() {
		return s.fallback.BackupCodes(ctx, unlockKey)
	}
	codes, err := s.primary.BackupCodes(ctx, unlockKey)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.BackupCodes(ctx, unlockKey)
	}
	return codes, err
}

func (s *daemonPreferredSecretStore) RecoveryCodes(ctx context.Context) ([]string, error) {
	if s.shouldUseFallback() {
		return s.fallback.RecoveryCodes(ctx)
	}
	codes, err := s.primary.RecoveryCodes(ctx)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.RecoveryCodes(ctx)
	}
	return codes, err
}

func (s *daemonPreferredSecretStore) RecoveryUnlocked(ctx context.Context) (bool, error) {
	if s.shouldUseFallback() {
		return s.fallback.RecoveryUnlocked(ctx)
	}
	unlocked, err := s.primary.RecoveryUnlocked(ctx)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.RecoveryUnlocked(ctx)
	}
	return unlocked, err
}

func (s *daemonPreferredSecretStore) RewrapAfterRecovery(ctx context.Context, newUnlockKey string) error {
	if s.shouldUseFallback() {
		return s.fallback.RewrapAfterRecovery(ctx, newUnlockKey)
	}
	err := s.primary.RewrapAfterRecovery(ctx, newUnlockKey)
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		s.enableFallback()
		return s.fallback.RewrapAfterRecovery(ctx, newUnlockKey)
	}
	return err
}

func (s *daemonPreferredSecretStore) shouldUseFallback() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.useFallback
}

func (s *daemonPreferredSecretStore) enableFallback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.useFallback = true
}
