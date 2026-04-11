package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type ToolRegistrySource struct {
	baseURL    string
	httpClient *http.Client
}

type toolRegistryInfo struct {
	Module        string `json:"module"`
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	GitSHA        string `json:"git_sha"`
	ManifestJSON  string `json:"manifest_json"`
}

func NewToolRegistrySource(baseURL string, client *http.Client) (*ToolRegistrySource, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("tool registry base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse tool registry base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("tool registry base URL %q must be an absolute http or https URL", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("tool registry base URL %q must use http or https", baseURL)
	}
	if client == nil {
		client = http.DefaultClient
	}

	return &ToolRegistrySource{
		baseURL:    strings.TrimRight(parsed.String(), "/"),
		httpClient: client,
	}, nil
}

func (s *ToolRegistrySource) Fetch(ctx context.Context, module ModulePath, version Version) (FetchResult, error) {
	infoURL := s.infoURL(module, version)
	info, err := s.fetchInfo(ctx, infoURL)
	if err != nil {
		return FetchResult{}, err
	}
	if err := info.validate(module, version); err != nil {
		return FetchResult{}, err
	}

	archiveURL := s.archiveURL(module, version)
	archiveBytes, err := s.getBytes(ctx, archiveURL)
	if err != nil {
		return FetchResult{}, err
	}
	if got := sha256Hex(archiveBytes); !strings.EqualFold(got, info.ArchiveSHA256) {
		return FetchResult{}, wrapToolRegistryUnavailable(nil, "GET %s: archive sha256 mismatch: got %s want %s", archiveURL, got, strings.ToLower(info.ArchiveSHA256))
	}

	manifestURL := s.manifestURL(module, version)
	manifestBytes, err := s.getBytes(ctx, manifestURL)
	if err != nil {
		return FetchResult{}, err
	}
	if string(manifestBytes) != info.ManifestJSON {
		return FetchResult{}, wrapToolRegistryUnavailable(nil, "GET %s: manifest bytes did not match manifest_json from %s", manifestURL, infoURL)
	}

	metadata := ResolveMetadata{
		ArchiveSHA256: strings.ToLower(info.ArchiveSHA256),
		GitSHA:        strings.ToLower(info.GitSHA),
		ResolvedFrom:  ResolvedFromToolRegistry,
		ResolvedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := metadata.Validate(); err != nil {
		return FetchResult{}, wrapToolRegistryUnavailable(err, "GET %s: invalid registry metadata", infoURL)
	}

	return FetchResult{
		Archive:  archiveBytes,
		Manifest: manifestBytes,
		Metadata: metadata,
	}, nil
}

func (s *ToolRegistrySource) ListVersions(ctx context.Context, module ModulePath) ([]Version, error) {
	requestURL := s.listURL(module)
	body, err := s.getBytes(ctx, requestURL)
	if err != nil {
		return nil, err
	}

	versions := make([]Version, 0)
	seen := make(map[Version]struct{})
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		version, err := tooldef.ParseVersion(line)
		if err != nil {
			continue
		}
		if _, ok := seen[version]; ok {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sortVersionsDesc(versions)
	return versions, nil
}

func (s *ToolRegistrySource) fetchInfo(ctx context.Context, requestURL string) (toolRegistryInfo, error) {
	body, err := s.getBytes(ctx, requestURL)
	if err != nil {
		return toolRegistryInfo{}, err
	}

	var info toolRegistryInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return toolRegistryInfo{}, wrapToolRegistryUnavailable(err, "GET %s: decode response", requestURL)
	}
	return info, nil
}

func (s *ToolRegistrySource) getBytes(ctx context.Context, requestURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", requestURL, err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, wrapToolRegistryUnavailable(err, "GET %s", requestURL)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapToolRegistryUnavailable(err, "GET %s: read response body", requestURL)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyToolRegistryHTTPError(requestURL, resp.StatusCode, body)
	}
	return body, nil
}

func (s *ToolRegistrySource) infoURL(module ModulePath, version Version) string {
	return s.requestURL(fmt.Sprintf("/v1/packages/%s/@v/%s.info", escapeModulePath(module), url.PathEscape(version.String())))
}

func (s *ToolRegistrySource) archiveURL(module ModulePath, version Version) string {
	return s.requestURL(fmt.Sprintf("/v1/packages/%s/@v/%s.pkg", escapeModulePath(module), url.PathEscape(version.String())))
}

func (s *ToolRegistrySource) manifestURL(module ModulePath, version Version) string {
	return s.requestURL(fmt.Sprintf("/v1/packages/%s/@v/%s.manifest", escapeModulePath(module), url.PathEscape(version.String())))
}

func (s *ToolRegistrySource) listURL(module ModulePath) string {
	return s.requestURL(fmt.Sprintf("/v1/packages/%s/@v/list", escapeModulePath(module)))
}

func (s *ToolRegistrySource) requestURL(path string) string {
	return s.baseURL + path
}

func (i toolRegistryInfo) validate(module ModulePath, version Version) error {
	switch {
	case i.Module == "":
		return wrapToolRegistryUnavailable(nil, "registry info for %s@%s omitted module", module, version)
	case i.Module != module.String():
		return wrapToolRegistryUnavailable(nil, "registry info module %q did not match requested module %q", i.Module, module)
	case i.Version == "":
		return wrapToolRegistryUnavailable(nil, "registry info for %s@%s omitted version", module, version)
	case i.Version != version.String():
		return wrapToolRegistryUnavailable(nil, "registry info version %q did not match requested version %q", i.Version, version)
	case !sha256HexPattern.MatchString(i.ArchiveSHA256):
		return wrapToolRegistryUnavailable(nil, "registry info archive_sha256 %q was invalid", i.ArchiveSHA256)
	case !gitCommitSHAPattern.MatchString(i.GitSHA):
		return wrapToolRegistryUnavailable(nil, "registry info git_sha %q was invalid", i.GitSHA)
	case strings.TrimSpace(i.ManifestJSON) == "":
		return wrapToolRegistryUnavailable(nil, "registry info for %s@%s omitted manifest_json", module, version)
	default:
		return nil
	}
}

func wrapToolRegistryUnavailable(cause error, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if cause == nil {
		return fmt.Errorf("%s: %w", message, ErrSourceUnavailable)
	}
	return fmt.Errorf("%s: %v: %w", message, cause, ErrSourceUnavailable)
}

func classifyToolRegistryHTTPError(requestURL string, statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(statusCode)
	}

	switch {
	case statusCode == http.StatusNotFound:
		return fmt.Errorf("GET %s: unexpected status %d: %s: %w", requestURL, statusCode, detail, ErrReleaseNotFound)
	case statusCode >= http.StatusInternalServerError:
		return wrapToolRegistryUnavailable(nil, "GET %s: unexpected status %d: %s", requestURL, statusCode, detail)
	default:
		return fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, statusCode, detail)
	}
}

func escapeModulePath(module ModulePath) string {
	return (&url.URL{Path: module.String()}).EscapedPath()
}

var _ PackageSource = (*ToolRegistrySource)(nil)
var _ VersionSource = (*ToolRegistrySource)(nil)
