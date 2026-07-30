package safeguard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	defaultRefreshInterval = 24 * time.Hour
	failedRefreshInterval  = time.Minute
)

type Client struct {
	policyURL string
	policyKey string
	http      *http.Client
	cachePath string
	now       func() time.Time

	mu             sync.Mutex
	cacheRead      bool
	hasPolicy      bool
	policy         Policy
	nextRefresh    time.Time
	lastRefreshErr error
}

type cacheEnvelope struct {
	SourceSHA256 string    `json:"source_sha256"`
	FetchedAt    time.Time `json:"fetched_at"`
	Policy       Policy    `json:"policy"`
}

// NewClient constructs a revocation-policy client. An empty cachePath uses
// the per-user Toolbox cache; passing "-" disables the disk cache for tests
// and embedded callers.
func NewClient(policyURL string, httpClient *http.Client, cachePath string) (*Client, error) {
	parsed, err := url.Parse(policyURL)
	if err != nil {
		return nil, fmt.Errorf("parse security policy URL %q: %w", policyURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("security policy URL %q must be an absolute http or https URL", policyURL)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	policyKey := fmt.Sprintf("%x", sha256.Sum256([]byte(parsed.String())))
	if cachePath == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("determine security policy cache dir: %w", err)
		}
		cachePath = filepath.Join(cacheDir, "toolbox", "security-policies", policyKey+".json")
	}
	return &Client{
		policyURL: parsed.String(),
		policyKey: policyKey,
		http:      httpClient,
		cachePath: cachePath,
		now:       time.Now,
	}, nil
}

// Refresh synchronously checks for a new policy when the current cached copy
// is due. A failed refresh never discards a previously valid policy.
func (c *Client) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.readCacheLocked()
	now := c.now().UTC()
	if now.Before(c.nextRefresh) {
		return c.lastRefreshErr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.policyURL, nil)
	if err != nil {
		return c.recordRefreshFailureLocked(now, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.recordRefreshFailureLocked(now, fmt.Errorf("GET %s: %w", c.policyURL, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return c.recordRefreshFailureLocked(now, fmt.Errorf("GET %s: unexpected status %d: %s", c.policyURL, resp.StatusCode, string(body)))
	}

	var policy Policy
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return c.recordRefreshFailureLocked(now, fmt.Errorf("GET %s: decode policy: %w", c.policyURL, err))
	}
	if err := policy.Validate(); err != nil {
		return c.recordRefreshFailureLocked(now, fmt.Errorf("GET %s: invalid policy: %w", c.policyURL, err))
	}

	c.policy = policy
	c.hasPolicy = true
	c.lastRefreshErr = nil
	c.nextRefresh = now.Add(defaultRefreshInterval)
	if err := c.writeCacheLocked(cacheEnvelope{SourceSHA256: c.policyKey, FetchedAt: now, Policy: policy}); err != nil {
		// The in-memory policy is valid and active. Report the durability issue
		// without treating the security service itself as unavailable.
		return err
	}
	return nil
}

func (c *Client) HasPolicy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readCacheLocked()
	return c.hasPolicy
}

func (c *Client) CheckToolbox(ctx context.Context, version tooldef.Version) error {
	_ = c.Refresh(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPolicy {
		return nil
	}
	return c.policy.CheckToolbox(version)
}

func (c *Client) CheckPackage(ctx context.Context, module tooldef.ModulePath, version tooldef.Version) error {
	_ = c.Refresh(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPolicy {
		return nil
	}
	return c.policy.CheckPackage(module, version)
}

func (c *Client) recordRefreshFailureLocked(now time.Time, err error) error {
	c.lastRefreshErr = err
	c.nextRefresh = now.Add(failedRefreshInterval)
	return err
}

func (c *Client) readCacheLocked() {
	if c.cacheRead {
		return
	}
	c.cacheRead = true
	if c.cachePath == "-" {
		return
	}
	data, err := os.ReadFile(c.cachePath)
	if err != nil {
		return
	}
	var cached cacheEnvelope
	if err := json.Unmarshal(data, &cached); err != nil ||
		cached.SourceSHA256 != c.policyKey ||
		cached.Policy.Validate() != nil {
		return
	}
	c.policy = cached.Policy
	c.hasPolicy = true
	c.nextRefresh = cached.FetchedAt.UTC().Add(defaultRefreshInterval)
}

func (c *Client) writeCacheLocked(cached cacheEnvelope) error {
	if c.cachePath == "-" {
		return nil
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return fmt.Errorf("marshal security policy cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o755); err != nil {
		return fmt.Errorf("create security policy cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.cachePath), ".security-policy-*")
	if err != nil {
		return fmt.Errorf("create security policy cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod security policy cache: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write security policy cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close security policy cache: %w", err)
	}
	if err := os.Rename(tmpName, c.cachePath); err != nil {
		return fmt.Errorf("replace security policy cache: %w", err)
	}
	return nil
}

var _ PackageGuard = (*Client)(nil)
