package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/testutil/fixtures"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestGitHubReleaseSource(t *testing.T) {
	srv := emulatetest.Start(t)
	src := NewGitHubReleaseSource(srv.BaseURL(), srv.Client())
	seed := srv.Seed()
	fixtureDir := fixtureSourceDir(t, "calc")

	t.Run("list versions", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/admin/stub-list/releases":
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": 3, "tag_name": "v1.2.0"},
					{"id": 2, "tag_name": "v1.0.0"},
					{"id": 1, "tag_name": "not-a-version"},
					{"id": 4, "tag_name": "v1.1.0"},
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		stub := NewGitHubReleaseSource(ts.URL, ts.Client())
		versions, err := stub.ListVersions(context.Background(), mustModulePath(t, "github.com/admin/stub-list"))
		if err != nil {
			t.Fatalf("ListVersions(): %v", err)
		}
		got := []string{versions[0].String(), versions[1].String(), versions[2].String()}
		want := []string{"v1.2.0", "v1.1.0", "v1.0.0"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("versions = %#v, want %#v", got, want)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		module := mustModulePath(t, "github.com/admin/stub-happy")
		version := mustVersion(t, "v1.0.0")
		archiveBytes := []byte("archive-bytes")
		manifestBytes := []byte(`{"name":"calc"}`)
		commitSHA := strings.Repeat("a", 40)

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/admin/stub-happy/releases/tags/v1.0.0":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":       10,
					"tag_name": "v1.0.0",
					"assets": []map[string]any{
						{"id": 1, "name": "calc.toolbox.pkg"},
						{"id": 2, "name": "toolbox.pkg.json"},
					},
				})
			case "/repos/admin/stub-happy/git/ref/tags/v1.0.0":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"object": map[string]any{"type": "commit", "sha": commitSHA},
				})
			case "/repos/admin/stub-happy/releases/assets/1":
				_, _ = w.Write(archiveBytes)
			case "/repos/admin/stub-happy/releases/assets/2":
				_, _ = w.Write(manifestBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		stub := NewGitHubReleaseSource(ts.URL, ts.Client())
		result, err := stub.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if len(result.Archive) == 0 {
			t.Fatal("Fetch(): archive bytes were empty")
		}
		if len(result.Manifest) == 0 {
			t.Fatal("Fetch(): manifest bytes were empty")
		}
		if result.Metadata.ResolvedFrom != ResolvedFromGitHubRelease {
			t.Fatalf("resolved_from = %q, want %q", result.Metadata.ResolvedFrom, ResolvedFromGitHubRelease)
		}
		if result.Metadata.ArchiveSHA256 != sha256Hex(result.Archive) {
			t.Fatalf("archive_sha256 = %q, want %q", result.Metadata.ArchiveSHA256, sha256Hex(result.Archive))
		}
		if result.Metadata.GitSHA != commitSHA {
			t.Fatalf("git_sha = %q, want %q", result.Metadata.GitSHA, commitSHA)
		}
		if _, err := time.Parse(time.RFC3339, result.Metadata.ResolvedAt); err != nil {
			t.Fatalf("resolved_at parse error: %v", err)
		}
	})

	t.Run("release not found", func(t *testing.T) {
		repo := nextSourceRepoName("not-found")
		module := mustModulePath(t, "github.com/admin/"+repo)
		version := mustVersion(t, "v9.9.9")

		_, err := seed.CreateRepo("admin", repo)
		if err != nil {
			t.Fatalf("CreateRepo(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("Fetch() error = %v, want errors.Is(..., ErrReleaseNotFound)", err)
		}
	})

	t.Run("missing archive asset", func(t *testing.T) {
		repo := nextSourceRepoName("missing-archive")
		module := mustModulePath(t, "github.com/admin/"+repo)
		version := mustVersion(t, "v1.0.1")

		_, err := seed.SeedMissingAssetRelease("admin", repo, version.String(), fixtureDir, "archive")
		if err != nil {
			t.Fatalf("SeedMissingAssetRelease(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "missing archive") {
			t.Fatalf("Fetch() error = %v, want missing archive message", err)
		}
	})

	t.Run("missing manifest asset", func(t *testing.T) {
		repo := nextSourceRepoName("missing-manifest")
		module := mustModulePath(t, "github.com/admin/"+repo)
		version := mustVersion(t, "v1.0.2")

		_, err := seed.SeedMissingAssetRelease("admin", repo, version.String(), fixtureDir, "manifest")
		if err != nil {
			t.Fatalf("SeedMissingAssetRelease(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "missing manifest") {
			t.Fatalf("Fetch() error = %v, want missing manifest message", err)
		}
	})

	t.Run("empty release", func(t *testing.T) {
		repo := nextSourceRepoName("empty")
		module := mustModulePath(t, "github.com/admin/"+repo)
		version := mustVersion(t, "v1.0.3")

		_, err := seed.SeedEmptyRelease("admin", repo, version.String())
		if err != nil {
			t.Fatalf("SeedEmptyRelease(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "has no assets") {
			t.Fatalf("Fetch() error = %v, want no assets message", err)
		}
	})

	t.Run("malformed tag lookup payload is rejected before metadata persistence", func(t *testing.T) {
		module := mustModulePath(t, "github.com/admin/stub")
		version := mustVersion(t, "v1.2.3")
		archiveBytes := []byte("archive-bytes")
		manifestBytes := []byte(`{"name":"calc"}`)

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/admin/stub/releases/tags/v1.2.3":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":       10,
					"tag_name": "v1.2.3",
					"assets": []map[string]any{
						{"id": 1, "name": "calc.toolbox.pkg"},
						{"id": 2, "name": "toolbox.pkg.json"},
					},
				})
			case "/repos/admin/stub/git/ref/tags/v1.2.3":
				_, _ = w.Write([]byte(`{"object":`))
			case "/repos/admin/stub/releases/assets/1":
				_, _ = w.Write(archiveBytes)
			case "/repos/admin/stub/releases/assets/2":
				_, _ = w.Write(manifestBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		stub := NewGitHubReleaseSource(ts.URL, ts.Client())
		_, err := stub.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "resolve git sha") {
			t.Fatalf("Fetch() error = %v, want git sha context", err)
		}
		if !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("Fetch() error = %v, want decode response", err)
		}
	})
}

func mustModulePath(t *testing.T, value string) ModulePath {
	t.Helper()
	module, err := tooldef.ParseModulePath(value)
	if err != nil {
		t.Fatalf("ParseModulePath(%q): %v", value, err)
	}
	return module
}

func mustVersion(t *testing.T, value string) Version {
	t.Helper()
	version, err := tooldef.ParseVersion(value)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", value, err)
	}
	return version
}

func looksLikeJSON(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func fixtureSourceDir(t *testing.T, fixtureName string) string {
	t.Helper()
	for _, dir := range fixtures.SourceDirs() {
		if filepath.Base(dir) == fixtureName {
			return dir
		}
	}
	t.Fatalf("source fixture %q not found", fixtureName)
	return ""
}

var sourceRepoCounter uint64

func nextSourceRepoName(prefix string) string {
	n := atomic.AddUint64(&sourceRepoCounter, 1)
	return fmt.Sprintf("%s-%03d", prefix, n)
}
