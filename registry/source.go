package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultGitHubAPIBaseURL = "https://api.github.com"

var ErrReleaseNotFound = errors.New("github release not found")

type PackageSource interface {
	Fetch(ctx context.Context, module ModulePath, version Version) (archive []byte, manifest []byte, err error)
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

func (s *GitHubReleaseSource) Fetch(ctx context.Context, module ModulePath, version Version) ([]byte, []byte, error) {
	owner, repo, err := githubRepoForModule(module)
	if err != nil {
		return nil, nil, err
	}

	tag := version.String()
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	release, err := s.fetchReleaseByTag(ctx, owner, repo, tag)
	if err != nil {
		return nil, nil, err
	}
	if len(release.Assets) == 0 {
		return nil, nil, fmt.Errorf("github release %s/%s@%s has no assets", owner, repo, tag)
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
		return nil, nil, fmt.Errorf("github release %s/%s@%s is missing archive asset ending in .toolbox.pkg", owner, repo, tag)
	}
	if manifestAsset == nil {
		return nil, nil, fmt.Errorf("github release %s/%s@%s is missing manifest asset toolbox.pkg.json", owner, repo, tag)
	}

	archiveBytes, err := s.downloadAsset(ctx, owner, repo, archiveAsset.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("download archive asset %q: %w", archiveAsset.Name, err)
	}
	manifestBytes, err := s.downloadAsset(ctx, owner, repo, manifestAsset.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("download manifest asset %q: %w", manifestAsset.Name, err)
	}

	return archiveBytes, manifestBytes, nil
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

var _ PackageSource = (*GitHubReleaseSource)(nil)
