package oauth2flow_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/oauth2flow"
	"golang.org/x/oauth2"
)

func TestRun(t *testing.T) {
	t.Parallel()

	// Mock token endpoint.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", r.FormValue("grant_type"))
		}
		if r.FormValue("code") != "test-code" {
			t.Errorf("code = %q, want test-code", r.FormValue("code"))
		}
		if r.FormValue("code_verifier") == "" {
			t.Error("code_verifier is empty")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	cfg := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://auth.example.com/authorize",
			TokenURL: tokenSrv.URL + "/token",
		},
		Scopes: []string{"read", "write"},
	}

	recv := &mockReceiver{
		uri:  "http://127.0.0.1:9999/callback",
		code: "test-code",
	}

	var capturedAuthURL string
	verifier := oauth2.GenerateVerifier()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tok, err := oauth2flow.Run(ctx, cfg, recv, verifier, nil, func(authURL string) {
		capturedAuthURL = authURL
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if tok.RefreshToken != "test-rt" {
		t.Fatalf("RefreshToken = %q, want test-rt", tok.RefreshToken)
	}
	if tok.AccessToken != "test-at" {
		t.Fatalf("AccessToken = %q, want test-at", tok.AccessToken)
	}

	// Verify auth URL was constructed.
	if capturedAuthURL == "" {
		t.Fatal("onAuthURL was not called")
	}
}
