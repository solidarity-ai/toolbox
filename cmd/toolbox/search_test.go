package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/registry"
)

func TestRunSearchDefaultsToPackageMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Fatalf("path = %q, want /v1/search", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(registry.PackageSearchResponse{
			OK: true,
			Hits: []registry.PackageSearchHit{{
				ModulePath:    "github.com/include-tools/calendars",
				LatestVersion: "v1.2.3",
				Runtime:       "typescript-sandbox",
				Name:          "calendars",
				Description:   "Calendar tools",
			}},
			Query: registry.SearchQuery{Q: "calendar", Limit: 20, Offset: 0},
		})
	}))
	defer ts.Close()

	t.Setenv("TOOLBOX_REGISTRY", ts.URL)

	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "calendar"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "MODULE") || !strings.Contains(got, "RUNTIME") {
		t.Fatalf("stdout = %q, want package search table", got)
	}
	if got := stdout.String(); !strings.Contains(got, "github.com/include-tools/calendars") || !strings.Contains(got, "Calendar tools") {
		t.Fatalf("stdout = %q, want package search hit", got)
	}
}

func TestRunSearchToolsMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/search" {
			t.Fatalf("path = %q, want /v1/tools/search", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(registry.ToolSearchResponse{
			OK: true,
			Hits: []registry.ToolSearchHit{{
				ModulePath:    "github.com/include-tools/calendars",
				LatestVersion: "v1.2.3",
				ToolPath:      "events.cancel",
				Effect:        "irreversible",
				PackageName:   "calendars",
				Description:   "Cancel an event",
			}},
			Query: registry.SearchQuery{Q: "cancel", Limit: 20, Offset: 0},
		})
	}))
	defer ts.Close()

	t.Setenv("TOOLBOX_REGISTRY", ts.URL)

	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "--tools", "cancel"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "TOOL") || !strings.Contains(got, "PACKAGE") {
		t.Fatalf("stdout = %q, want tool search table", got)
	}
	if got := stdout.String(); !strings.Contains(got, "events.cancel") || !strings.Contains(got, "Cancel an event") {
		t.Fatalf("stdout = %q, want tool search hit", got)
	}
}

func TestRunSearchJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(registry.PackageSearchResponse{
			OK: true,
			Hits: []registry.PackageSearchHit{{
				ModulePath:    "github.com/include-tools/calendars",
				LatestVersion: "v1.2.3",
				Runtime:       "typescript-sandbox",
				Name:          "calendars",
			}},
			Query: registry.SearchQuery{
				Q:      "calendar",
				Limit:  20,
				Offset: 0,
			},
		})
	}))
	defer ts.Close()

	t.Setenv("TOOLBOX_REGISTRY", ts.URL)

	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "--json", "calendar"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}

	var got registry.PackageSearchResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout): %v\nstdout=%s", err, stdout.String())
	}
	if got.Query.Q != "calendar" || len(got.Hits) != 1 {
		t.Fatalf("response = %#v, want query and hit preserved", got)
	}
}

func TestRunSearchRejectsConflictingModeFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "--packages", "--tools", "calendar"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "--tools and --packages are mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive flags", err)
	}
}

func TestRunSearchFailsWhenRegistryDisabled(t *testing.T) {
	t.Setenv("TOOLBOX_REGISTRY", "off")

	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "calendar"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "registry is disabled") {
		t.Fatalf("error = %v, want registry disabled context", err)
	}
}

func TestRunSearchSurfacesRegistry400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "effect=destructive is not a canonical value; use effect=irreversible",
		})
	}))
	defer ts.Close()

	t.Setenv("TOOLBOX_REGISTRY", ts.URL)

	var stdout, stderr bytes.Buffer
	err := run([]string{"search", "--effect", "destructive", "users"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want non-nil")
	}
	if got := err.Error(); got != "effect=destructive is not a canonical value; use effect=irreversible" {
		t.Fatalf("error = %q, want registry canonicalization message", got)
	}
}
