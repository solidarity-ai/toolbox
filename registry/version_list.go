package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// VersionSource lists available versions for a module from a specific backing source.
type VersionSource interface {
	ListVersions(ctx context.Context, module ModulePath) ([]Version, error)
}

func (s *GitHubReleaseSource) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	owner, repo, err := githubRepoForModule(module)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", url.PathEscape(owner), url.PathEscape(repo))
	requestURL := s.requestURL(path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build releases request: %w", err)
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
		return nil, fmt.Errorf("list releases for %s/%s: %w", owner, repo, ErrReleaseNotFound)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}

	versions := make([]Version, 0, len(releases))
	seen := make(map[Version]struct{}, len(releases))
	for _, release := range releases {
		version, err := tooldef.ParseVersion(release.TagName)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	tooldef.SortVersionsDesc(versions)
	return versions, nil
}

func (s *GitSourceFallback) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	cloneURL := s.cloneURL(module)
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", cloneURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, wrapGitCommandError(fmt.Sprintf("git ls-remote tags %s from %s", module, cloneURL), err, strings.TrimSpace(string(output)))
	}

	versions := make([]Version, 0)
	seen := make(map[Version]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ref := fields[1]
		if !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if strings.HasSuffix(tag, "^{}") {
			tag = strings.TrimSuffix(tag, "^{}")
		}
		version, err := tooldef.ParseVersion(tag)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	tooldef.SortVersionsDesc(versions)
	return versions, nil
}

var _ VersionSource = (*GitHubReleaseSource)(nil)
var _ VersionSource = (*GitSourceFallback)(nil)
