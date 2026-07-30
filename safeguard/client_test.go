package safeguard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRetainsCachedPolicyWhenRefreshFails(t *testing.T) {
	policy := Policy{
		SchemaVersion: SchemaVersion,
		Toolbox:       []ToolboxRevocation{{Version: "v1.0.0", Reason: "known vulnerable release"}},
	}
	available := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !available {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(policy)
	}))
	defer server.Close()

	cachePath := filepath.Join(t.TempDir(), "policy.json")
	first, err := NewClient(server.URL, server.Client(), cachePath)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	available = false
	second, err := NewClient(server.URL, server.Client(), cachePath)
	if err != nil {
		t.Fatalf("NewClient(second) error = %v", err)
	}
	second.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	var blocked *BlockedError
	if err := second.CheckToolbox(context.Background(), "v1.0.0"); !errors.As(err, &blocked) {
		t.Fatalf("CheckToolbox() error = %v, want cached *BlockedError", err)
	}
}

func TestClientDoesNotReuseCacheForDifferentPolicyURL(t *testing.T) {
	policy := Policy{
		SchemaVersion: SchemaVersion,
		Toolbox:       []ToolboxRevocation{{Version: "v1.0.0", Reason: "known vulnerable release"}},
	}
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(policy)
	}))
	defer firstServer.Close()

	cachePath := filepath.Join(t.TempDir(), "policy.json")
	first, err := NewClient(firstServer.URL, firstServer.Client(), cachePath)
	if err != nil {
		t.Fatalf("NewClient(first) error = %v", err)
	}
	if err := first.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh(first) error = %v", err)
	}

	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer secondServer.Close()
	second, err := NewClient(secondServer.URL, secondServer.Client(), cachePath)
	if err != nil {
		t.Fatalf("NewClient(second) error = %v", err)
	}
	if err := second.CheckToolbox(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("CheckToolbox() error = %v, want cache from another URL ignored", err)
	}
	if second.HasPolicy() {
		t.Fatal("HasPolicy() = true, want cache from another URL ignored")
	}
}

func TestClientDefaultCachePathIsNamespacedByPolicyURL(t *testing.T) {
	const policyURL = "https://packages.include.tools/v1/security/policy"
	client, err := NewClient(policyURL, nil, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir() error = %v", err)
	}
	want := filepath.Join(cacheDir, "toolbox", "security-policies", client.policyKey+".json")
	if client.cachePath != want {
		t.Fatalf("cachePath = %q, want %q", client.cachePath, want)
	}
}

func TestClientRefreshesAfterTwentyFourHours(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(Policy{SchemaVersion: SchemaVersion})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client(), "-")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	base := time.Date(2026, time.July, 16, 0, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return base }
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh(first) error = %v", err)
	}
	client.now = func() time.Time { return base.Add(24*time.Hour - time.Second) }
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh(before interval) error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests before interval = %d, want 1", got)
	}

	client.now = func() time.Time { return base.Add(24 * time.Hour) }
	if err := client.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh(at interval) error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests at interval = %d, want 2", got)
	}
}

func TestClientFailsOpenWithoutAnyValidPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client(), "-")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := client.CheckToolbox(context.Background(), "v1.0.0"); err != nil {
		t.Fatalf("CheckToolbox() error = %v, want fail-open nil", err)
	}
}
