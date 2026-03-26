package emulatetest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
)

const testOwner = "admin"

var repoCounter uint64

func TestMain(m *testing.M) {
	code := m.Run()
	shutdownSharedServer()
	os.Exit(code)
}

func TestEmulateLifecycle(t *testing.T) {
	server := Start(t)
	again := Start(t)

	if server.BaseURL() == "" {
		t.Fatal("BaseURL() returned empty string")
	}
	if server.Port() == 0 {
		t.Fatal("Port() returned 0")
	}
	if server.Port() != again.Port() {
		t.Fatalf("Start() did not reuse the shared server: first=%d second=%d", server.Port(), again.Port())
	}

	resp, err := server.Client().Get(server.BaseURL() + "/rate_limit")
	if err != nil {
		t.Fatalf("GET /rate_limit: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /rate_limit: want %d, got %d", http.StatusOK, resp.StatusCode)
	}

	t.Logf("emulate started on port %d", server.Port())
	t.Logf("GET %s/rate_limit -> %d", server.BaseURL(), resp.StatusCode)
}

func TestSeedRepoAndRelease(t *testing.T) {
	server := Start(t)
	seed := server.Seed()
	repoName := nextRepoName("repo")

	repo, err := seed.CreateRepo(testOwner, repoName)
	if err != nil {
		t.Fatalf("CreateRepo(): %v", err)
	}
	if repo.ID <= 0 {
		t.Fatalf("CreateRepo(): expected repo ID > 0, got %d", repo.ID)
	}
	if diff := cmp.Diff(fmt.Sprintf("%s/%s", testOwner, repoName), repo.FullName); diff != "" {
		t.Fatalf("CreateRepo() full_name mismatch (-want +got):\n%s", diff)
	}

	release, err := seed.CreateRelease(testOwner, repoName, "v1.0.0")
	if err != nil {
		t.Fatalf("CreateRelease(): %v", err)
	}
	if release.ID <= 0 {
		t.Fatalf("CreateRelease(): expected release ID > 0, got %d", release.ID)
	}
	if diff := cmp.Diff("v1.0.0", release.TagName); diff != "" {
		t.Fatalf("CreateRelease() tag mismatch (-want +got):\n%s", diff)
	}

	t.Logf("POST /user/repos -> %s (id=%d)", repo.FullName, repo.ID)
	t.Logf("POST /repos/%s/%s/releases -> id=%d tag=%s", testOwner, repoName, release.ID, release.TagName)
}

func TestSeedPackageRelease(t *testing.T) {
	server := Start(t)
	repoName := nextRepoName("pkg")

	result, err := server.Seed().SeedPackageRelease(testOwner, repoName, "v1.0.0", fixtureSourceDir(t, "calc"))
	if err != nil {
		t.Fatalf("SeedPackageRelease(): %v", err)
	}
	if len(result.ArchiveBytes) == 0 {
		t.Fatal("SeedPackageRelease(): ArchiveBytes was empty")
	}
	if len(result.ManifestBytes) == 0 {
		t.Fatal("SeedPackageRelease(): ManifestBytes was empty")
	}
	if result.ReleaseID <= 0 || result.ArchiveAssetID <= 0 || result.ManifestAssetID <= 0 {
		t.Fatalf("SeedPackageRelease(): invalid IDs: %+v", result)
	}

	release := fetchReleaseByTag(t, server, result.Owner, result.Repo, result.Tag)
	if len(release.Assets) != 2 {
		t.Fatalf("GET release by tag: want 2 assets, got %d", len(release.Assets))
	}
	if diff := cmp.Diff(result.Tag, release.TagName); diff != "" {
		t.Fatalf("release tag mismatch (-want +got):\n%s", diff)
	}

	assetNames := make([]string, 0, len(release.Assets))
	for _, asset := range release.Assets {
		assetNames = append(assetNames, asset.Name)
		if asset.Size <= 0 {
			t.Fatalf("asset %q had non-positive size %d", asset.Name, asset.Size)
		}
	}
	sort.Strings(assetNames)
	wantNames := []string{"calc.toolbox.pkg", "toolbox.pkg.json"}
	if diff := cmp.Diff(wantNames, assetNames); diff != "" {
		t.Fatalf("release asset names mismatch (-want +got):\n%s", diff)
	}

	t.Logf("seeded %s/%s@%s with release id=%d", result.Owner, result.Repo, result.Tag, result.ReleaseID)
	t.Logf("GET /repos/%s/%s/releases/tags/%s -> %d assets", result.Owner, result.Repo, result.Tag, len(release.Assets))
	t.Logf("release metadata verified for assets %v", assetNames)
}

func TestSeedPackageReleaseMissingSourceDir(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := (&SeedClient{}).SeedPackageRelease(testOwner, "unused", "v1.0.0", missingDir)
	if err == nil {
		t.Fatal("SeedPackageRelease() succeeded for a missing source dir")
	}
	if !strings.Contains(err.Error(), missingDir) {
		t.Fatalf("SeedPackageRelease() error did not mention missing dir %q: %v", missingDir, err)
	}
	if !strings.Contains(err.Error(), "toolbox.devpkg.json") {
		t.Fatalf("SeedPackageRelease() error did not mention missing manifest: %v", err)
	}
}

func TestCreateReleaseMissingRepo(t *testing.T) {
	server := Start(t)
	_, err := server.Seed().CreateRelease(testOwner, nextRepoName("missing"), "v0.0.1")
	if err == nil {
		t.Fatal("CreateRelease() succeeded for a missing repo")
	}
	if !strings.Contains(err.Error(), "unexpected status 404") {
		t.Fatalf("CreateRelease() error did not include status code: %v", err)
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("CreateRelease() error did not include response body: %v", err)
	}

	t.Logf("missing repo returned expected error: %v", err)
}

func fetchReleaseByTag(t *testing.T, server *Server, owner, repo, tag string) Release {
	t.Helper()

	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", server.BaseURL(), owner, repo, tag)
	resp, err := server.Client().Get(url)
	if err != nil {
		t.Fatalf("GET release by tag: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET release by tag: read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET release by tag: want %d, got %d: %s", http.StatusOK, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var release Release
	if err := json.Unmarshal(raw, &release); err != nil {
		t.Fatalf("GET release by tag: decode JSON: %v\nbody=%s", err, raw)
	}
	return release
}

func fixtureSourceDir(t *testing.T, name string) string {
	t.Helper()
	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == name {
			return dir
		}
	}
	t.Fatalf("fixture source dir %q not found", name)
	return ""
}

func nextRepoName(prefix string) string {
	n := atomic.AddUint64(&repoCounter, 1)
	return fmt.Sprintf("%s-%03d", prefix, n)
}
