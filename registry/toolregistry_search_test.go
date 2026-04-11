package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolRegistrySearchClientSearchPackages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Fatalf("path = %q, want /v1/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "calendar sync" {
			t.Fatalf("q = %q, want %q", got, "calendar sync")
		}
		if got := r.URL.Query().Get("runtime"); got != "typescript-sandbox" {
			t.Fatalf("runtime = %q, want %q", got, "typescript-sandbox")
		}
		if got := r.URL.Query().Get("effect"); got != "irreversible" {
			t.Fatalf("effect = %q, want %q", got, "irreversible")
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Fatalf("limit = %q, want 5", got)
		}
		if got := r.URL.Query().Get("offset"); got != "10" {
			t.Fatalf("offset = %q, want 10", got)
		}
		_ = json.NewEncoder(w).Encode(PackageSearchResponse{
			OK: true,
			Hits: []PackageSearchHit{{
				ModulePath:    "github.com/include-tools/calendars",
				Name:          "calendars",
				Runtime:       "typescript-sandbox",
				Description:   "Calendar tools",
				LatestVersion: "v1.2.3",
				Rank:          -1.25,
			}},
			Query: SearchQuery{
				Q:       "calendar sync",
				Runtime: "typescript-sandbox",
				Effect:  "irreversible",
				Limit:   5,
				Offset:  10,
			},
		})
	}))
	defer ts.Close()

	client, err := NewToolRegistrySearchClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatalf("NewToolRegistrySearchClient(): %v", err)
	}

	response, err := client.SearchPackages(context.Background(), SearchQuery{
		Q:       "calendar sync",
		Runtime: "typescript-sandbox",
		Effect:  "irreversible",
		Limit:   5,
		Offset:  10,
	})
	if err != nil {
		t.Fatalf("SearchPackages(): %v", err)
	}
	if !response.OK {
		t.Fatal("response.OK = false, want true")
	}
	if len(response.Hits) != 1 || response.Hits[0].ModulePath != "github.com/include-tools/calendars" {
		t.Fatalf("hits = %#v, want calendar package hit", response.Hits)
	}
	if response.Query.Limit != 5 || response.Query.Offset != 10 {
		t.Fatalf("query = %#v, want preserved limit/offset", response.Query)
	}
}

func TestToolRegistrySearchClientSearchTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/search" {
			t.Fatalf("path = %q, want /v1/tools/search", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(ToolSearchResponse{
			OK: true,
			Hits: []ToolSearchHit{{
				ModulePath:     "github.com/include-tools/calendars",
				LatestVersion:  "v1.2.3",
				ToolPath:       "events.cancel",
				Name:           "events.cancel",
				Description:    "Cancel an event",
				Effect:         "irreversible",
				PackageName:    "calendars",
				PackageRuntime: "typescript-sandbox",
				Rank:           -0.75,
			}},
			Query: SearchQuery{
				Q:      "cancel",
				Limit:  20,
				Offset: 0,
			},
		})
	}))
	defer ts.Close()

	client, err := NewToolRegistrySearchClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatalf("NewToolRegistrySearchClient(): %v", err)
	}

	response, err := client.SearchTools(context.Background(), SearchQuery{Q: "cancel", Limit: 20, Offset: 0})
	if err != nil {
		t.Fatalf("SearchTools(): %v", err)
	}
	if len(response.Hits) != 1 || response.Hits[0].ToolPath != "events.cancel" {
		t.Fatalf("hits = %#v, want events.cancel hit", response.Hits)
	}
	if response.Hits[0].PackageName != "calendars" {
		t.Fatalf("packageName = %q, want calendars", response.Hits[0].PackageName)
	}
}

func TestToolRegistrySearchClientErrors(t *testing.T) {
	t.Run("400 returns registry message directly", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "effect=destructive is not a canonical value; use effect=irreversible",
			})
		}))
		defer ts.Close()

		client, err := NewToolRegistrySearchClient(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySearchClient(): %v", err)
		}

		_, err = client.SearchPackages(context.Background(), SearchQuery{Q: "users", Effect: "destructive", Limit: 20, Offset: 0})
		if err == nil {
			t.Fatal("SearchPackages() error = nil, want non-nil")
		}
		if got := err.Error(); got != "effect=destructive is not a canonical value; use effect=irreversible" {
			t.Fatalf("error = %q, want registry message", got)
		}
	})

	t.Run("5xx returns contextual error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer ts.Close()

		client, err := NewToolRegistrySearchClient(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySearchClient(): %v", err)
		}

		_, err = client.SearchPackages(context.Background(), SearchQuery{Q: "users", Limit: 20, Offset: 0})
		if err == nil {
			t.Fatal("SearchPackages() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "/v1/search?") || !strings.Contains(err.Error(), "q=users") || !strings.Contains(err.Error(), "limit=20") || !strings.Contains(err.Error(), "offset=0") || !strings.Contains(err.Error(), "503") {
			t.Fatalf("error = %q, want contextual 503 error", err)
		}
	})

	t.Run("malformed success response fails decode", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}))
		defer ts.Close()

		client, err := NewToolRegistrySearchClient(ts.URL, ts.Client())
		if err != nil {
			t.Fatalf("NewToolRegistrySearchClient(): %v", err)
		}

		_, err = client.SearchTools(context.Background(), SearchQuery{Q: "cancel", Limit: 20, Offset: 0})
		if err == nil {
			t.Fatal("SearchTools() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("error = %q, want decode response context", err)
		}
	})
}
