package emulatetest

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/solidarity-ai/toolbox/packaging"
)

const defaultRepoOwner = "admin"

// Repo is the small subset of repository metadata the tests care about.
type Repo struct {
	ID       int    `json:"id"`
	FullName string `json:"full_name"`
}

// Release is the small subset of release metadata the tests care about.
type Release struct {
	ID        int     `json:"id"`
	TagName   string  `json:"tag_name"`
	UploadURL string  `json:"upload_url"`
	Assets    []Asset `json:"assets"`
}

// Issue is the small subset of issue metadata the tests care about.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

// Asset is the small subset of release asset metadata the tests care about.
type Asset struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Size        int    `json:"size"`
	ContentType string `json:"content_type"`
}

// SeedResult captures the uploaded assets and the raw bytes used to create
// them. emulate v0.3.0 reports release asset metadata correctly, but its asset
// download path does not reliably return the uploaded binary bytes yet.
type SeedResult struct {
	Owner           string
	Repo            string
	Tag             string
	ReleaseID       int
	ArchiveAssetID  int
	ManifestAssetID int
	ArchiveBytes    []byte
	ManifestBytes   []byte
}

// SeedClient creates repos, releases, and release assets inside emulate.
type SeedClient struct {
	baseURL string
	client  *http.Client
}

// Seed returns a client preconfigured for the running emulate server.
func (s *Server) Seed() *SeedClient {
	return &SeedClient{
		baseURL: s.baseURL,
		client:  s.client,
	}
}

// CreateRepo creates a repo via /user/repos. emulate v0.3.0 auto-creates the
// admin user but does not expose the org creation endpoints, so owner is kept
// for API symmetry and future callers even though repos land under admin today.
func (c *SeedClient) CreateRepo(owner, name string) (*Repo, error) {
	var repo Repo
	if err := c.doJSON(http.MethodPost, "/user/repos", map[string]any{"name": name}, &repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

// CreateRelease creates a GitHub release for the given repo/tag.
func (c *SeedClient) CreateRelease(owner, repo, tag string) (*Release, error) {
	var release Release
	path := fmt.Sprintf("/repos/%s/%s/releases", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.doJSON(http.MethodPost, path, map[string]any{"tag_name": tag}, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// CreateIssue creates an issue for the given repository.
func (c *SeedClient) CreateIssue(owner, repo, title string) (*Issue, error) {
	var issue Issue
	path := fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.doJSON(http.MethodPost, path, map[string]any{"title": title}, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// UploadReleaseAsset uploads a binary asset to the release.
func (c *SeedClient) UploadReleaseAsset(owner, repo string, releaseID int, filename string, data []byte) (*Asset, error) {
	var asset Asset
	path := fmt.Sprintf(
		"/repos/%s/%s/releases/%d/assets?name=%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		releaseID,
		url.QueryEscape(filename),
	)
	if err := c.doBytes(http.MethodPost, path, data, "application/octet-stream", &asset); err != nil {
		return nil, err
	}
	return &asset, nil
}

// SeedPackageRelease packs a real fixture package and uploads both the archive
// and compiled manifest as release assets.
func (c *SeedClient) SeedPackageRelease(owner, repo, tag, pkgDir string) (*SeedResult, error) {
	packed, err := packReleaseAssets(pkgDir)
	if err != nil {
		return nil, err
	}
	return c.seedReleaseAssets(owner, repo, tag, packed.archiveName, packed.archiveBytes, packed.manifestName, packed.manifestBytes)
}

// SeedCorruptArchiveRelease uploads a valid manifest with intentionally corrupt
// archive bytes so callers can exercise archive extraction failures.
func (c *SeedClient) SeedCorruptArchiveRelease(owner, repo, tag, pkgDir string) (*SeedResult, error) {
	packed, err := packReleaseAssets(pkgDir)
	if err != nil {
		return nil, err
	}

	corruptArchiveBytes := make([]byte, len(packed.archiveBytes))
	if _, err := crand.Read(corruptArchiveBytes); err != nil {
		return nil, fmt.Errorf("generate corrupt archive bytes: %w", err)
	}

	return c.seedReleaseAssets(owner, repo, tag, packed.archiveName, corruptArchiveBytes, packed.manifestName, packed.manifestBytes)
}

// SeedMismatchedHashRelease uploads the real archive with a tampered manifest
// sha256 so callers can exercise checksum mismatch paths.
func (c *SeedClient) SeedMismatchedHashRelease(owner, repo, tag, pkgDir string) (*SeedResult, error) {
	packed, err := packReleaseAssets(pkgDir)
	if err != nil {
		return nil, err
	}

	tamperedManifestBytes, err := tamperManifestSHA(packed.manifestBytes)
	if err != nil {
		return nil, err
	}

	return c.seedReleaseAssets(owner, repo, tag, packed.archiveName, packed.archiveBytes, packed.manifestName, tamperedManifestBytes)
}

// SeedMissingAssetRelease uploads only one release asset. mode must be
// "archive" to omit the archive and leave the manifest, or "manifest" to omit
// the manifest and leave the archive.
func (c *SeedClient) SeedMissingAssetRelease(owner, repo, tag, pkgDir, mode string) (*SeedResult, error) {
	packed, err := packReleaseAssets(pkgDir)
	if err != nil {
		return nil, err
	}

	switch mode {
	case "archive":
		return c.seedReleaseAssets(owner, repo, tag, "", nil, packed.manifestName, packed.manifestBytes)
	case "manifest":
		return c.seedReleaseAssets(owner, repo, tag, packed.archiveName, packed.archiveBytes, "", nil)
	default:
		return nil, fmt.Errorf("unsupported missing asset mode %q", mode)
	}
}

// SeedEmptyRelease creates a repo and release without uploading any assets.
func (c *SeedClient) SeedEmptyRelease(owner, repo, tag string) (*SeedResult, error) {
	return c.seedReleaseAssets(owner, repo, tag, "", nil, "", nil)
}

func (c *SeedClient) seedReleaseAssets(owner, repo, tag, archiveName string, archiveBytes []byte, manifestName string, manifestBytes []byte) (*SeedResult, error) {
	resolvedOwner, err := c.ensureRepo(owner, repo)
	if err != nil {
		return nil, err
	}

	release, err := c.CreateRelease(resolvedOwner, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("create release %s/%s@%s: %w", resolvedOwner, repo, tag, err)
	}

	result := &SeedResult{
		Owner:         resolvedOwner,
		Repo:          repo,
		Tag:           tag,
		ReleaseID:     release.ID,
		ArchiveBytes:  cloneBytes(archiveBytes),
		ManifestBytes: cloneBytes(manifestBytes),
	}

	if archiveName != "" {
		archiveAsset, err := c.UploadReleaseAsset(resolvedOwner, repo, release.ID, archiveName, archiveBytes)
		if err != nil {
			return nil, fmt.Errorf("upload archive asset: %w", err)
		}
		result.ArchiveAssetID = archiveAsset.ID
	}
	if manifestName != "" {
		manifestAsset, err := c.UploadReleaseAsset(resolvedOwner, repo, release.ID, manifestName, manifestBytes)
		if err != nil {
			return nil, fmt.Errorf("upload manifest asset: %w", err)
		}
		result.ManifestAssetID = manifestAsset.ID
	}

	return result, nil
}

func (c *SeedClient) ensureRepo(owner, repo string) (string, error) {
	resolvedOwner := owner
	if resolvedOwner == "" {
		resolvedOwner = defaultRepoOwner
	}

	repoResult, err := c.CreateRepo(resolvedOwner, repo)
	if err != nil {
		if !isAlreadyExistsError(err) {
			return "", fmt.Errorf("create repo %s/%s: %w", resolvedOwner, repo, err)
		}
		return defaultRepoOwner, nil
	}
	if parts := strings.SplitN(repoResult.FullName, "/", 2); len(parts) == 2 && parts[0] != "" {
		resolvedOwner = parts[0]
	}
	return resolvedOwner, nil
}

type packedReleaseAssets struct {
	archiveName   string
	archiveBytes  []byte
	manifestName  string
	manifestBytes []byte
}

func packReleaseAssets(pkgDir string) (*packedReleaseAssets, error) {
	outDir, err := os.MkdirTemp("", "emulatetest-pack-*")
	if err != nil {
		return nil, fmt.Errorf("create pack temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	packResult, err := packaging.Pack(pkgDir, outDir)
	if err != nil {
		return nil, fmt.Errorf("pack source package %q: %w", pkgDir, err)
	}

	archiveBytes, err := os.ReadFile(packResult.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("read archive %q: %w", packResult.ArchivePath, err)
	}
	manifestBytes, err := os.ReadFile(packResult.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", packResult.ManifestPath, err)
	}

	return &packedReleaseAssets{
		archiveName:   filepath.Base(packResult.ArchivePath),
		archiveBytes:  archiveBytes,
		manifestName:  filepath.Base(packResult.ManifestPath),
		manifestBytes: manifestBytes,
	}, nil
}

func tamperManifestSHA(manifestBytes []byte) ([]byte, error) {
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest for tampering: %w", err)
	}

	shaValue, _ := manifest["sha256"].(string)
	if shaValue == "" {
		return nil, fmt.Errorf("manifest missing sha256 for tampering")
	}
	manifest["sha256"] = mutateHexString(shaValue)

	tamperedManifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode tampered manifest: %w", err)
	}
	return append(tamperedManifestBytes, '\n'), nil
}

func mutateHexString(value string) string {
	if value == "" {
		return value
	}
	var replacement byte = '0'
	if value[0] == '0' {
		replacement = '1'
	}
	return string(replacement) + value[1:]
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

func (c *SeedClient) doJSON(method, path string, payload any, into any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("%s %s: marshal request: %w", method, path, err)
		}
	}
	return c.do(method, path, body, "application/json", into)
}

func (c *SeedClient) doBytes(method, path string, payload []byte, contentType string, into any) error {
	return c.do(method, path, payload, contentType, into)
}

func (c *SeedClient) do(method, path string, payload []byte, contentType string, into any) error {
	requestURL := c.requestURL(path)
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, requestURL, body)
	if err != nil {
		return fmt.Errorf("%s %s: build request: %w", method, requestURL, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, requestURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read response body: %w", method, requestURL, err)
	}
	bodyText := strings.TrimSpace(string(raw))

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s: unexpected status %d: %s", method, requestURL, resp.StatusCode, bodyText)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s %s: decode response: %w; body=%s", method, requestURL, err, bodyText)
	}
	return nil
}

func (c *SeedClient) requestURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(c.baseURL, "/") + path
}

func isAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "Repository already exists")
}
