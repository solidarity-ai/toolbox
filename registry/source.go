package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultGitHubAPIBaseURL = "https://api.github.com"

var (
	ErrReleaseNotFound   = errors.New("release not found")
	ErrSourceUnavailable = errors.New("source unavailable")
	sha256HexPattern     = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	gitCommitSHAPattern  = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type ResolvedFrom string

const (
	ResolvedFromGitHubRelease ResolvedFrom = "github-release"
	ResolvedFromGitSource     ResolvedFrom = "git-source"
	ResolvedFromToolRegistry  ResolvedFrom = "tool-registry"
)

type ResolveMetadata struct {
	ArchiveSHA256 string       `json:"archive_sha256"`
	GitSHA        string       `json:"git_sha"`
	ResolvedFrom  ResolvedFrom `json:"resolved_from"`
	ResolvedAt    string       `json:"resolved_at"`
}

func (m ResolveMetadata) Validate() error {
	if !sha256HexPattern.MatchString(m.ArchiveSHA256) {
		return fmt.Errorf("archive_sha256 %q must be a 64-character hex sha256", m.ArchiveSHA256)
	}
	if !gitCommitSHAPattern.MatchString(m.GitSHA) {
		return fmt.Errorf("git_sha %q must be a 40-character hex git commit", m.GitSHA)
	}
	switch m.ResolvedFrom {
	case ResolvedFromGitHubRelease, ResolvedFromGitSource, ResolvedFromToolRegistry:
		// okay
	default:
		return fmt.Errorf("resolved_from %q is invalid", m.ResolvedFrom)
	}
	if _, err := time.Parse(time.RFC3339, m.ResolvedAt); err != nil {
		return fmt.Errorf("resolved_at %q must be RFC3339: %w", m.ResolvedAt, err)
	}
	return nil
}

type FetchResult struct {
	Archive  []byte
	Manifest []byte
	Metadata ResolveMetadata
}

type PackageSource interface {
	Fetch(ctx context.Context, module ModulePath, version Version) (FetchResult, error)
}

type GitHubReleaseSource struct {
	baseURL    string
	httpClient *http.Client
}

type githubRelease struct {
	ID      int                  `json:"id"`
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type githubRef struct {
	Object githubRefObject `json:"object"`
}

type githubRefObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func NewGitHubReleaseSource(baseURL string, client *http.Client) *GitHubReleaseSource {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGitHubAPIBaseURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubReleaseSource{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: client,
	}
}

func (s *GitHubReleaseSource) Fetch(ctx context.Context, module ModulePath, version Version) (FetchResult, error) {
	owner, repo, err := githubRepoForModule(module)
	if err != nil {
		return FetchResult{}, err
	}

	tag := version.String()
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	release, err := s.fetchReleaseByTag(ctx, owner, repo, tag)
	if err != nil {
		return FetchResult{}, err
	}
	if len(release.Assets) == 0 {
		return FetchResult{}, fmt.Errorf("github release %s/%s@%s has no assets", owner, repo, tag)
	}

	var archiveAsset *githubReleaseAsset
	var manifestAsset *githubReleaseAsset
	for i := range release.Assets {
		asset := &release.Assets[i]
		switch {
		case strings.HasSuffix(asset.Name, ".toolbox.pkg"):
			if archiveAsset == nil {
				archiveAsset = asset
			}
		case asset.Name == "toolbox.pkg.json":
			if manifestAsset == nil {
				manifestAsset = asset
			}
		}
	}

	if archiveAsset == nil {
		return FetchResult{}, fmt.Errorf("github release %s/%s@%s is missing archive asset ending in .toolbox.pkg", owner, repo, tag)
	}
	if manifestAsset == nil {
		return FetchResult{}, fmt.Errorf("github release %s/%s@%s is missing manifest asset toolbox.pkg.json", owner, repo, tag)
	}

	gitSHA, err := s.fetchTagCommitSHA(ctx, owner, repo, tag)
	if err != nil {
		return FetchResult{}, fmt.Errorf("resolve git sha for github release %s/%s@%s: %w", owner, repo, tag, err)
	}

	archiveBytes, err := s.downloadAsset(ctx, owner, repo, archiveAsset.ID)
	if err != nil {
		return FetchResult{}, fmt.Errorf("download archive asset %q: %w", archiveAsset.Name, err)
	}
	manifestBytes, err := s.downloadAsset(ctx, owner, repo, manifestAsset.ID)
	if err != nil {
		return FetchResult{}, fmt.Errorf("download manifest asset %q: %w", manifestAsset.Name, err)
	}

	metadata := ResolveMetadata{
		ArchiveSHA256: sha256Hex(archiveBytes),
		GitSHA:        gitSHA,
		ResolvedFrom:  ResolvedFromGitHubRelease,
		ResolvedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := metadata.Validate(); err != nil {
		return FetchResult{}, fmt.Errorf("github release %s/%s@%s returned invalid metadata: %w", owner, repo, tag, err)
	}

	return FetchResult{Archive: archiveBytes, Manifest: manifestBytes, Metadata: metadata}, nil
}

func (s *GitHubReleaseSource) fetchReleaseByTag(ctx context.Context, owner, repo, tag string) (*githubRelease, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	requestURL := s.requestURL(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build release request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read response body: %w", requestURL, err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s/%s@%s: %w", owner, repo, tag, ErrReleaseNotFound)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}
	return &release, nil
}

func (s *GitHubReleaseSource) fetchTagCommitSHA(ctx context.Context, owner, repo, tag string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/git/ref/tags/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	requestURL := s.requestURL(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build tag ref request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("GET %s: read response body: %w", requestURL, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var ref githubRef
	if err := json.Unmarshal(body, &ref); err != nil {
		return "", fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}
	if ref.Object.Type != "commit" {
		return "", fmt.Errorf("GET %s: expected tag ref object type %q, got %q", requestURL, "commit", ref.Object.Type)
	}
	if !gitCommitSHAPattern.MatchString(ref.Object.SHA) {
		return "", fmt.Errorf("GET %s: invalid commit sha %q", requestURL, ref.Object.SHA)
	}
	return strings.ToLower(ref.Object.SHA), nil
}

func (s *GitHubReleaseSource) downloadAsset(ctx context.Context, owner, repo string, assetID int) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/assets/%d", url.PathEscape(owner), url.PathEscape(repo), assetID)
	requestURL := s.requestURL(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build asset request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read response body: %w", requestURL, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (s *GitHubReleaseSource) requestURL(path string) string {
	return s.baseURL + path
}

func githubRepoForModule(module ModulePath) (owner, repo string, err error) {
	parts := strings.Split(module.String(), "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("module path %q must contain host/owner/repo", module)
	}
	if parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("module path %q must contain non-empty owner and repo", module)
	}
	return parts[1], parts[2], nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

var _ PackageSource = (*GitHubReleaseSource)(nil)
