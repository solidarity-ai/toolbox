package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestToolRegistrySourceFetch(t *testing.T) {
	archiveBytes, manifestBytes := loadDistFixtureBytes(t, "calc-dist")
	module := mustModulePath(t, "github.com/admin/stub")
	version := mustVersion(t, "v1.0.0")
	const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	t.Run("happy path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.info":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"module":         module.String(),
					"version":        version.String(),
					"archive_sha256": sha256Hex(archiveBytes),
					"git_sha":        commitSHA,
					"manifest_json":  string(manifestBytes),
				})
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.pkg":
				http.Redirect(w, r, "/assets/calc.toolbox.pkg", http.StatusFound)
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.manifest":
				http.Redirect(w, r, "/assets/toolbox.pkg.json", http.StatusFound)
			case "/assets/calc.toolbox.pkg":
				_, _ = w.Write(archiveBytes)
			case "/assets/toolbox.pkg.json":
				_, _ = w.Write(manifestBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		result, err := src.Fetch(context.Background(), module, version)
		if err != nil {
			t.Fatalf("Fetch(): %v", err)
		}
		if result.Metadata.ResolvedFrom != ResolvedFromToolRegistry {
			t.Fatalf("resolved_from = %q, want %q", result.Metadata.ResolvedFrom, ResolvedFromToolRegistry)
		}
		if result.Metadata.ArchiveSHA256 != sha256Hex(archiveBytes) {
			t.Fatalf("archive_sha256 = %q, want %q", result.Metadata.ArchiveSHA256, sha256Hex(archiveBytes))
		}
		if result.Metadata.GitSHA != commitSHA {
			t.Fatalf("git_sha = %q, want %q", result.Metadata.GitSHA, commitSHA)
		}
		if !reflect.DeepEqual(result.Archive, archiveBytes) {
			t.Fatal("archive bytes were not preserved")
		}
		if !reflect.DeepEqual(result.Manifest, manifestBytes) {
			t.Fatal("manifest bytes were not preserved")
		}
	})

	t.Run("info 404 maps to not found", func(t *testing.T) {
		ts := httptest.NewServer(http.NotFoundHandler())
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("error = %v, want ErrReleaseNotFound", err)
		}
	})

	t.Run("malformed info maps to source unavailable", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.info":
				_, _ = w.Write([]byte("{"))
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("error = %v, want ErrSourceUnavailable", err)
		}
	})

	t.Run("checksum mismatch maps to source unavailable", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.info":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"module":         module.String(),
					"version":        version.String(),
					"archive_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					"git_sha":        commitSHA,
					"manifest_json":  string(manifestBytes),
				})
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.pkg":
				_, _ = w.Write(archiveBytes)
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.manifest":
				_, _ = w.Write(manifestBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("error = %v, want ErrSourceUnavailable", err)
		}
	})

	t.Run("manifest mismatch maps to source unavailable", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.info":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"module":         module.String(),
					"version":        version.String(),
					"archive_sha256": sha256Hex(archiveBytes),
					"git_sha":        commitSHA,
					"manifest_json":  `{"name":"wrong"}`,
				})
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.pkg":
				_, _ = w.Write(archiveBytes)
			case "/v1/packages/github.com/admin/stub/@v/v1.0.0.manifest":
				_, _ = w.Write(manifestBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("error = %v, want ErrSourceUnavailable", err)
		}
	})

	t.Run("403 stays a hard error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.Fetch(context.Background(), module, version)
		if err == nil {
			t.Fatal("Fetch() error = nil, want non-nil")
		}
		if errors.Is(err, ErrReleaseNotFound) || errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("error = %v, want hard error", err)
		}
	})
}

func TestToolRegistrySourceListVersions(t *testing.T) {
	module := mustModulePath(t, "github.com/admin/stub")

	t.Run("parses and sorts versions", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/packages/github.com/admin/stub/@v/list":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte("v1.0.0\nnot-a-version\nv1.2.0\nv1.0.0\n"))
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		versions, err := src.ListVersions(context.Background(), module)
		if err != nil {
			t.Fatalf("ListVersions(): %v", err)
		}
		got := []string{versions[0].String(), versions[1].String()}
		want := []string{"v1.2.0", "v1.0.0"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("versions = %#v, want %#v", got, want)
		}
	})

	t.Run("404 maps to not found", func(t *testing.T) {
		ts := httptest.NewServer(http.NotFoundHandler())
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.ListVersions(context.Background(), module)
		if err == nil {
			t.Fatal("ListVersions() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Fatalf("error = %v, want ErrReleaseNotFound", err)
		}
	})

	t.Run("503 maps to source unavailable", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer ts.Close()

		src, err := NewToolRegistrySource(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySource(): %v", err)
		}

		_, err = src.ListVersions(context.Background(), module)
		if err == nil {
			t.Fatal("ListVersions() error = nil, want non-nil")
		}
		if !errors.Is(err, ErrSourceUnavailable) {
			t.Fatalf("error = %v, want ErrSourceUnavailable", err)
		}
	})
}
