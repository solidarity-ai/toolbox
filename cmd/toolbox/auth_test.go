package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/packaging"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func newTestSecretStore(t *testing.T) secrets.SecretStore {
	t.Helper()
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")
	store := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, "test-secret-key")
	store.SetBackupCodeWriter(io.Discard)
	return store
}

func newTestCredentialRepo(t *testing.T) (*credentialrepo.Repository, secrets.SecretStore) {
	t.Helper()
	store := newTestSecretStore(t)
	return credentialrepo.New(store), store
}

func TestRunAuthAPIKeyEndToEnd(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "weather_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("sk-test-key-12345\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	// Verify the key was stored under accounts/default/.
	val, err := store.Get(context.Background(), credpath.APIKey(testModuleString("my-package"), "weather_api", "default"))
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(val) != "sk-test-key-12345" {
		t.Fatalf("stored api_key = %q, want %q", string(val), "sk-test-key-12345")
	}

	// Verify output.
	out := stdout.String()
	if !strings.Contains(out, "Enter API key for weather_api: ") {
		t.Fatalf("stdout = %q, want prompt for API key", out)
	}
	if !strings.Contains(out, "Authorized weather_api (api_key)") {
		t.Fatalf("stdout = %q, want authorization confirmation", out)
	}
	if !strings.Contains(out, "api_key: secret store ("+credpath.APIKey(testModuleString("my-package"), "weather_api", "default")+")") {
		t.Fatalf("stdout = %q, want provenance line", out)
	}
	if !strings.Contains(out, "Credentials stored. Tools in this package can now make authenticated requests.") {
		t.Fatalf("stdout = %q, want next steps message", out)
	}
}

func TestRunAuthShowsCredentialInstructions(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name:         "weather_api",
					Type:         "api_key",
					Instructions: "Get an API key from https://example.com/settings/api-keys.",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("sk-test-key-12345\n")
	var stdout, stderr bytes.Buffer

	if err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", ""); err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Instructions:\n  Get an API key from https://example.com/settings/api-keys.\n\nEnter API key for weather_api: ") {
		t.Fatalf("stdout = %q, want credential instructions before prompt", out)
	}
}

func TestRunAuthNoCredentials(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("simple-package"),
			Name:    "simple-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader(""), &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no credentials required") {
		t.Fatalf("stdout = %q, want 'no credentials required'", stdout.String())
	}
}

func TestRunAuthBearerToken(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "github_token",
					Type: "bearer",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.github.com"},
						Method: "bearer_header",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("ghp-my-token-value\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	val, err := store.Get(context.Background(), credpath.BearerToken(testModuleString("my-package"), "github_token", "default"))
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(val) != "ghp-my-token-value" {
		t.Fatalf("stored token = %q, want %q", string(val), "ghp-my-token-value")
	}

	out := stdout.String()
	if !strings.Contains(out, "Authorized github_token (bearer)") {
		t.Fatalf("stdout = %q, want authorization confirmation", out)
	}
	if !strings.Contains(out, "token: secret store ("+credpath.BearerToken(testModuleString("my-package"), "github_token", "default")+")") {
		t.Fatalf("stdout = %q, want provenance line", out)
	}
	if !strings.Contains(out, "Credentials stored. Tools in this package can now make authenticated requests.") {
		t.Fatalf("stdout = %q, want next steps message", out)
	}
}

func TestRunAuthBearerTokenRejectsEmptyOnFirstSetup(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "github_token",
				Type: "bearer",
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.github.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader("\n"), &stdout, &stderr, "", "")
	if err == nil {
		t.Fatal("expected error for empty bearer token")
	}
	if !strings.Contains(err.Error(), "token cannot be empty") {
		t.Fatalf("error = %v, want 'token cannot be empty'", err)
	}

	_, err = store.Get(context.Background(), credpath.BearerToken(testModuleString("my-package"), "github_token", "default"))
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestRunAuthBearerBasicAuth(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "jira_auth",
					Type: "bearer",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"jira.example.com"},
						Method: "basic_auth",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("admin\nsecretpass\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	ctx := context.Background()
	username, err := store.Get(ctx, credpath.BearerUsername(testModuleString("my-package"), "jira_auth", "default"))
	if err != nil {
		t.Fatalf("Get(username) error: %v", err)
	}
	if string(username) != "admin" {
		t.Fatalf("stored username = %q, want %q", string(username), "admin")
	}

	password, err := store.Get(ctx, credpath.BearerPassword(testModuleString("my-package"), "jira_auth", "default"))
	if err != nil {
		t.Fatalf("Get(password) error: %v", err)
	}
	if string(password) != "secretpass" {
		t.Fatalf("stored password = %q, want %q", string(password), "secretpass")
	}

	out := stdout.String()
	if !strings.Contains(out, "Authorized jira_auth (bearer)") {
		t.Fatalf("stdout = %q, want authorization confirmation", out)
	}
}

func TestRunAuthBearerBasicAuthRejectsEmptyOnFirstSetup(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError string
	}{
		{name: "EmptyUsername", input: "\nsecretpass\n", wantError: "username cannot be empty"},
		{name: "EmptyPassword", input: "admin\n\n", wantError: "password cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, store := newTestCredentialRepo(t)

			loaded := packaging.LoadedPackage{
				Package: tooldef.Package{
					Module:  testModule("my-package"),
					Name:    "my-package",
					Runtime: tooldef.RuntimeTypeScriptSandbox,
					Credentials: []tooldef.PackageCredential{{
						Name: "jira_auth",
						Type: "bearer",
						Inject: tooldef.PackageInject{
							Hosts:  []string{"jira.example.com"},
							Method: "basic_auth",
						},
					}},
				},
			}

			var stdout, stderr bytes.Buffer
			err := runAuthWithRepo(loaded, repo, strings.NewReader(tt.input), &stdout, &stderr, "", "")
			if err == nil {
				t.Fatal("expected error for empty basic auth secret")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}

			ctx := context.Background()
			_, err = store.Get(ctx, credpath.BearerUsername(testModuleString("my-package"), "jira_auth", "default"))
			if !errors.Is(err, secrets.ErrNotFound) {
				t.Fatalf("Get(username) error = %v, want ErrNotFound", err)
			}
			_, err = store.Get(ctx, credpath.BearerPassword(testModuleString("my-package"), "jira_auth", "default"))
			if !errors.Is(err, secrets.ErrNotFound) {
				t.Fatalf("Get(password) error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestRunAuthAPIKeyRejectsEmpty(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "empty_key",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"example.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "")
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
	if !strings.Contains(err.Error(), "api key cannot be empty") {
		t.Fatalf("error = %v, want 'api key cannot be empty'", err)
	}
}

func TestRunAuthMissingArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runAuth(nil, secretStoreOptions{NoDaemon: true, SecretKey: "test-secret-key"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing args")
	}
	if !strings.Contains(err.Error(), "expected package directory argument") {
		t.Fatalf("error = %v, want 'expected package directory argument'", err)
	}
}

func TestRunAuthCheckFlag(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "weather_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
				{
					Name: "github_token",
					Type: "bearer",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.github.com"},
						Method: "bearer_header",
					},
				},
			},
		},
	}

	// Seed only one credential under accounts/default/.
	ctx := context.Background()
	if err := store.Set(ctx, credpath.APIKey(testModuleString("my-package"), "weather_api", "default"), []byte("test-key")); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := checkAuthWithRepo(loaded, repo, &stdout)
	if err != nil {
		t.Fatalf("checkAuthWithRepo() error: %v", err)
	}

	out := stdout.String()
	// weather_api should be configured.
	if !strings.Contains(out, "weather_api (api_key): \u2713 configured") {
		t.Errorf("expected weather_api configured, got: %s", out)
	}
	// github_token should not be configured.
	if !strings.Contains(out, "github_token (bearer): \u2717 not configured") {
		t.Errorf("expected github_token not configured, got: %s", out)
	}
	// Should mention toolbox auth for the missing one.
	if !strings.Contains(out, "toolbox auth") {
		t.Errorf("expected 'toolbox auth' hint, got: %s", out)
	}
}

func TestRunAuthCheckTreatsEmptyStoredSecretAsMissing(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "github_token",
				Type: "bearer",
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.github.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	if err := store.Set(context.Background(), credpath.BearerToken(testModuleString("my-package"), "github_token", "default"), []byte("")); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := checkAuthWithRepo(loaded, repo, &stdout)
	if err != nil {
		t.Fatalf("checkAuthWithRepo() error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "github_token (bearer): ✗ not configured") {
		t.Fatalf("stdout = %q, want empty secret treated as missing", out)
	}
}

func TestRunAuthCheckShowsCredentialInstructionsWhenMissing(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name:         "github_token",
				Type:         "bearer",
				Instructions: "Create a personal access token in GitHub settings.",
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.github.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	var stdout bytes.Buffer
	if err := checkAuthWithRepo(loaded, repo, &stdout); err != nil {
		t.Fatalf("checkAuthWithRepo() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "github_token (bearer): ✗ not configured") {
		t.Fatalf("stdout = %q, want missing credential status", out)
	}
	if !strings.Contains(out, "  Instructions:\n    Create a personal access token in GitHub settings.\n") {
		t.Fatalf("stdout = %q, want credential instructions in check output", out)
	}
}

func TestRunAuthCheckFlag_OAuth2PublicClientDoesNotRequireClientSecret(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-public-check"),
			Name:    "oauth-public-check",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  "https://example.com/auth",
					TokenURL: "https://example.com/token",
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-public-check"), "test_oauth"), []byte("cid")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-public-check"), "test_oauth", "default"), []byte("rt")); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := checkAuthWithRepo(loaded, repo, &stdout)
	if err != nil {
		t.Fatalf("checkAuthWithRepo() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "test_oauth (oauth2): ✓ configured") {
		t.Fatalf("expected oauth2 credential configured, got: %s", out)
	}
	if !strings.Contains(out, "client_secret: optional (PKCE public client)") {
		t.Fatalf("expected optional client_secret line, got: %s", out)
	}
}

func TestRunAuthAPIKeyKeepExisting(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "weather_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	// Pre-seed the store under accounts/default/.
	ctx := context.Background()
	if err := store.Set(ctx, credpath.APIKey(testModuleString("my-package"), "weather_api", "default"), []byte("original-key")); err != nil {
		t.Fatal(err)
	}

	// User presses Enter (empty input) to keep existing.
	stdin := strings.NewReader("\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	// Verify the original key is preserved.
	val, err := store.Get(ctx, credpath.APIKey(testModuleString("my-package"), "weather_api", "default"))
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(val) != "original-key" {
		t.Fatalf("stored api_key = %q, want %q", string(val), "original-key")
	}

	out := stdout.String()
	if !strings.Contains(out, "already configured") {
		t.Errorf("expected 'already configured' prompt, got: %s", out)
	}
	if !strings.Contains(out, "Keeping existing") {
		t.Errorf("expected 'Keeping existing' message, got: %s", out)
	}
}

func TestRunAuthOAuth2EndToEnd(t *testing.T) {
	// Track what the mock token endpoint receives.
	var (
		mu                   sync.Mutex
		receivedGrantType    string
		receivedCode         string
		receivedClientID     string
		receivedClientSecret string
		receivedCodeVerifier string
		receivedRedirectURI  string
	)

	// Start a mock token endpoint.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		mu.Lock()
		receivedGrantType = r.FormValue("grant_type")
		receivedCode = r.FormValue("code")
		// The oauth2 library may send client credentials via Basic Auth
		// header (RFC 6749 §2.3.1) or form body. Check both.
		receivedClientID = r.FormValue("client_id")
		receivedClientSecret = r.FormValue("client_secret")
		if receivedClientID == "" {
			receivedClientID, receivedClientSecret, _ = r.BasicAuth()
		}
		receivedCodeVerifier = r.FormValue("code_verifier")
		receivedRedirectURI = r.FormValue("redirect_uri")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	// Start a mock auth endpoint (just needs to exist; the real browser would redirect).
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Not actually called in the flow; the test simulates the callback directly.
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	// Set up the secret store and seed client_id and client_secret.
	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()

	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-e2e-test"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-e2e-test"), "test_oauth"), []byte("test-client-secret")); err != nil {
		t.Fatal(err)
	}

	// Build the loaded package with OAuth2 credential pointing to our mock servers.
	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-e2e-test"),
			Name:    "oauth-e2e-test",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "test_oauth",
					Type: "oauth2",
					Provider: &tooldef.OAuth2ProviderConfig{
						AuthURL:  authServer.URL + "/authorize",
						TokenURL: tokenServer.URL + "/token",
					},
					Scopes: []string{"read", "write"},
					Inject: tooldef.PackageInject{
						Hosts:      []string{"api.example.com"},
						Method:     "bearer_header",
						PathPrefix: "/v1/",
					},
				},
			},
		},
	}

	// Override openBrowser to capture the auth URL and simulate the OAuth callback.
	originalOpenBrowser := openBrowser
	defer func() { openBrowser = originalOpenBrowser }()

	var capturedAuthURL string
	callbackDone := make(chan struct{})

	openBrowser = func(authURL string) {
		capturedAuthURL = authURL

		// Parse the auth URL to extract the redirect_uri (which contains the callback port).
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("failed to parse auth URL: %v", err)
			return
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		if redirectURI == "" {
			t.Errorf("auth URL missing redirect_uri parameter")
			return
		}

		// Extract state from the auth URL to include in the callback (CSRF protection).
		state := parsed.Query().Get("state")

		// Simulate the OAuth provider redirecting back with an auth code + state.
		go func() {
			defer close(callbackDone)
			callbackURL := fmt.Sprintf("%s?code=test-auth-code&state=%s", redirectURI, state)
			resp, err := http.Get(callbackURL)
			if err != nil {
				t.Errorf("callback request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}

	// Run the auth flow. No stdin input needed since client_id/secret are pre-seeded
	// and there's no existing refresh_token to prompt about.
	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader(""), &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	// Wait for the callback goroutine to finish.
	<-callbackDone

	// Assert the refresh_token was stored under accounts/default/.
	rt, err := store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-e2e-test"), "test_oauth", "default"))
	if err != nil {
		t.Fatalf("refresh_token not stored: %v", err)
	}
	if string(rt) != "test-rt" {
		t.Fatalf("stored refresh_token = %q, want %q", string(rt), "test-rt")
	}

	// Assert output contains expected messages.
	out := stdout.String()
	if !strings.Contains(out, "Credentials stored") {
		t.Errorf("stdout missing 'Credentials stored', got: %s", out)
	}
	if !strings.Contains(out, "read") || !strings.Contains(out, "write") {
		t.Errorf("stdout missing scopes, got: %s", out)
	}
	if !strings.Contains(out, "Authorizing test_oauth (oauth2)") {
		t.Errorf("stdout missing authorization header, got: %s", out)
	}

	// Assert the token endpoint received the correct values.
	mu.Lock()
	defer mu.Unlock()

	if receivedGrantType != "authorization_code" {
		t.Errorf("token endpoint grant_type = %q, want %q", receivedGrantType, "authorization_code")
	}
	if receivedCode != "test-auth-code" {
		t.Errorf("token endpoint code = %q, want %q", receivedCode, "test-auth-code")
	}
	if receivedClientID != "test-client-id" {
		t.Errorf("token endpoint client_id = %q, want %q", receivedClientID, "test-client-id")
	}
	if receivedClientSecret != "test-client-secret" {
		t.Errorf("token endpoint client_secret = %q, want %q", receivedClientSecret, "test-client-secret")
	}
	if receivedCodeVerifier == "" {
		t.Error("token endpoint code_verifier was empty")
	}
	if receivedRedirectURI == "" {
		t.Error("token endpoint redirect_uri was empty")
	}

	// Verify the PKCE code_verifier is valid by checking it against the challenge
	// that was sent in the auth URL.
	if capturedAuthURL != "" {
		parsed, err := url.Parse(capturedAuthURL)
		if err != nil {
			t.Fatalf("failed to parse captured auth URL: %v", err)
		}
		codeChallenge := parsed.Query().Get("code_challenge")
		codeChallengeMethod := parsed.Query().Get("code_challenge_method")

		if codeChallengeMethod != "S256" {
			t.Errorf("code_challenge_method = %q, want %q", codeChallengeMethod, "S256")
		}
		if codeChallenge == "" {
			t.Error("code_challenge was empty in auth URL")
		}

		// Verify: SHA256(code_verifier) base64url-encoded == code_challenge
		h := sha256.Sum256([]byte(receivedCodeVerifier))
		expectedChallenge := base64.RawURLEncoding.EncodeToString(h[:])
		if codeChallenge != expectedChallenge {
			t.Errorf("PKCE verification failed: challenge = %q, expected %q (from verifier %q)",
				codeChallenge, expectedChallenge, receivedCodeVerifier)
		}
	}
}

func TestRunAuthOAuth2PublicClientAllowsBlankSecret(t *testing.T) {
	var (
		mu                   sync.Mutex
		receivedGrantType    string
		receivedCode         string
		receivedClientID     string
		receivedClientSecret string
		receivedCodeVerifier string
	)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		mu.Lock()
		receivedGrantType = r.FormValue("grant_type")
		receivedCode = r.FormValue("code")
		receivedClientID = r.FormValue("client_id")
		receivedClientSecret = r.FormValue("client_secret")
		if receivedClientID == "" {
			receivedClientID, receivedClientSecret, _ = r.BasicAuth()
		}
		receivedCodeVerifier = r.FormValue("code_verifier")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-public"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-public"),
			Name:    "oauth-public",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  authServer.URL + "/authorize",
					TokenURL: tokenServer.URL + "/token",
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	originalOpenBrowser := openBrowser
	defer func() { openBrowser = originalOpenBrowser }()

	callbackDone := make(chan struct{})
	openBrowser = func(authURL string) {
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("failed to parse auth URL: %v", err)
			return
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")

		go func() {
			defer close(callbackDone)
			callbackURL := fmt.Sprintf("%s?code=test-auth-code&state=%s", redirectURI, state)
			resp, err := http.Get(callbackURL)
			if err != nil {
				t.Errorf("callback request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader("\n"), &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}
	<-callbackDone

	if _, err := store.Get(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-public"), "test_oauth")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(client_secret) error = %v, want ErrNotFound", err)
	}
	refreshToken, err := store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-public"), "test_oauth", "default"))
	if err != nil {
		t.Fatalf("Get(refresh_token) error: %v", err)
	}
	if string(refreshToken) != "test-rt" {
		t.Fatalf("stored refresh_token = %q, want %q", string(refreshToken), "test-rt")
	}

	out := stdout.String()
	if !strings.Contains(out, "press Enter to skip for PKCE public clients") {
		t.Fatalf("stdout missing public-client prompt, got: %s", out)
	}
	if !strings.Contains(out, "client_secret: not used (PKCE public client)") {
		t.Fatalf("stdout missing public-client provenance, got: %s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedGrantType != "authorization_code" {
		t.Fatalf("grant_type = %q, want %q", receivedGrantType, "authorization_code")
	}
	if receivedCode != "test-auth-code" {
		t.Fatalf("code = %q, want %q", receivedCode, "test-auth-code")
	}
	if receivedClientID != "test-client-id" {
		t.Fatalf("client_id = %q, want %q", receivedClientID, "test-client-id")
	}
	if receivedClientSecret != "" {
		t.Fatalf("client_secret = %q, want empty", receivedClientSecret)
	}
	if receivedCodeVerifier == "" {
		t.Fatal("code_verifier was empty")
	}
}

func TestRunAuthOAuth2WithoutPKCERequiresClientSecret(t *testing.T) {
	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-no-pkce"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}

	pkce := false
	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-no-pkce"),
			Name:    "oauth-no-pkce",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  "https://example.com/auth",
					TokenURL: "https://example.com/token",
					PKCE:     &pkce,
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader("\n"), &stdout, &stderr, "", "")
	if err == nil {
		t.Fatal("expected error for missing client_secret when PKCE is disabled")
	}
	if !strings.Contains(err.Error(), "client_secret cannot be empty") {
		t.Fatalf("error = %v, want 'client_secret cannot be empty'", err)
	}
}

func TestRunAuthOAuth2WithoutPKCEDisablesChallengeAndVerifier(t *testing.T) {
	var (
		mu                   sync.Mutex
		receivedCodeVerifier string
		capturedAuthURL      string
	)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		receivedCodeVerifier = r.FormValue("code_verifier")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-no-pkce"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-no-pkce"), "test_oauth"), []byte("test-client-secret")); err != nil {
		t.Fatal(err)
	}

	pkce := false
	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-no-pkce"),
			Name:    "oauth-no-pkce",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  authServer.URL + "/authorize",
					TokenURL: tokenServer.URL + "/token",
					PKCE:     &pkce,
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	originalOpenBrowser := openBrowser
	defer func() { openBrowser = originalOpenBrowser }()

	callbackDone := make(chan struct{})
	openBrowser = func(authURL string) {
		capturedAuthURL = authURL
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("failed to parse auth URL: %v", err)
			return
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")

		go func() {
			defer close(callbackDone)
			callbackURL := fmt.Sprintf("%s?code=test-auth-code&state=%s", redirectURI, state)
			resp, err := http.Get(callbackURL)
			if err != nil {
				t.Errorf("callback request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader(""), &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}
	<-callbackDone

	mu.Lock()
	gotVerifier := receivedCodeVerifier
	mu.Unlock()
	if gotVerifier != "" {
		t.Fatalf("code_verifier = %q, want empty", gotVerifier)
	}
	if capturedAuthURL == "" {
		t.Fatal("capturedAuthURL was empty")
	}
	parsed, err := url.Parse(capturedAuthURL)
	if err != nil {
		t.Fatalf("failed to parse captured auth URL: %v", err)
	}
	if parsed.Query().Get("code_challenge") != "" {
		t.Fatalf("code_challenge = %q, want empty", parsed.Query().Get("code_challenge"))
	}
	if parsed.Query().Get("code_challenge_method") != "" {
		t.Fatalf("code_challenge_method = %q, want empty", parsed.Query().Get("code_challenge_method"))
	}
}

func TestRunAuthOAuth2ManualPasteHeadless(t *testing.T) {
	var (
		mu                sync.Mutex
		receivedCode      string
		receivedGrantType string
	)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		receivedGrantType = r.FormValue("grant_type")
		receivedCode = r.FormValue("code")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-headless"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-headless"), "test_oauth"), []byte("test-client-secret")); err != nil {
		t.Fatal(err)
	}

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-headless"),
			Name:    "oauth-headless",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "test_oauth",
					Type: "oauth2",
					Provider: &tooldef.OAuth2ProviderConfig{
						AuthURL:  authServer.URL + "/authorize",
						TokenURL: tokenServer.URL + "/token",
					},
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.example.com"},
						Method: "bearer_header",
					},
				},
			},
		},
	}

	originalOpenBrowser := openBrowser
	defer func() { openBrowser = originalOpenBrowser }()

	reader, writer := io.Pipe()
	defer reader.Close()

	var openedAuthURL string
	openBrowser = func(authURL string) {
		openedAuthURL = authURL
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("failed to parse auth URL: %v", err)
			return
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")

		go func() {
			callbackURL := fmt.Sprintf("%s?code=manual-auth-code&state=%s", redirectURI, state)
			if _, err := io.WriteString(writer, callbackURL+"\n"); err != nil {
				t.Errorf("write manual callback URL: %v", err)
			}
			_ = writer.Close()
		}()
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, reader, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	refreshToken, err := store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-headless"), "test_oauth", "default"))
	if err != nil {
		t.Fatalf("Get(refresh_token) error: %v", err)
	}
	if string(refreshToken) != "test-rt" {
		t.Fatalf("stored refresh_token = %q, want %q", string(refreshToken), "test-rt")
	}

	mu.Lock()
	gotGrantType := receivedGrantType
	gotCode := receivedCode
	mu.Unlock()
	if gotGrantType != "authorization_code" {
		t.Fatalf("grant_type = %q, want %q", gotGrantType, "authorization_code")
	}
	if gotCode != "manual-auth-code" {
		t.Fatalf("code = %q, want %q", gotCode, "manual-auth-code")
	}
	if openedAuthURL == "" {
		t.Fatal("openBrowser was not called")
	}
	if !strings.Contains(stdout.String(), "Or paste the full redirect URL or authorization code here:") {
		t.Fatalf("stdout missing manual paste prompt, got: %s", stdout.String())
	}
}

func TestRunAuthOAuth2CallbackDoesNotConsumeNextCredentialInput(t *testing.T) {
	var (
		mu           sync.Mutex
		grantCount   int
		callbackDone = make(chan struct{})
	)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		grantCount++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "test-at",
			"refresh_token": "test-rt",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()
	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-next"), "test_oauth"), []byte("test-client-id")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-next"), "test_oauth"), []byte("test-client-secret")); err != nil {
		t.Fatal(err)
	}

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-next"),
			Name:    "oauth-next",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "test_oauth",
					Type: "oauth2",
					Provider: &tooldef.OAuth2ProviderConfig{
						AuthURL:  authServer.URL + "/authorize",
						TokenURL: tokenServer.URL + "/token",
					},
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.example.com"},
						Method: "bearer_header",
					},
				},
				{
					Name: "weather_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	reader, writer := io.Pipe()
	defer reader.Close()

	originalOpenBrowser := openBrowser
	defer func() { openBrowser = originalOpenBrowser }()

	openBrowser = func(authURL string) {
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("failed to parse auth URL: %v", err)
			return
		}
		redirectURI := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")

		go func() {
			defer close(callbackDone)
			callbackURL := fmt.Sprintf("%s?code=test-auth-code&state=%s", redirectURI, state)
			resp, err := http.Get(callbackURL)
			if err != nil {
				t.Errorf("callback request failed: %v", err)
				return
			}
			resp.Body.Close()

			// A late pasted auth code URL must be ignored by the next prompt. The
			// actual next credential input that follows still needs to be read.
			if _, err := io.WriteString(writer, callbackURL+"\nnext-api-key\n"); err != nil {
				t.Errorf("write api key input: %v", err)
			}
			_ = writer.Close()
		}()
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, reader, &stdout, &stderr, "", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}
	<-callbackDone

	apiKey, err := store.Get(ctx, credpath.APIKey(testModuleString("oauth-next"), "weather_api", "default"))
	if err != nil {
		t.Fatalf("Get(api_key) error: %v", err)
	}
	if string(apiKey) != "next-api-key" {
		t.Fatalf("stored api_key = %q, want %q", string(apiKey), "next-api-key")
	}
	refreshToken, err := store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-next"), "test_oauth", "default"))
	if err != nil {
		t.Fatalf("Get(refresh_token) error: %v", err)
	}
	if string(refreshToken) != "test-rt" {
		t.Fatalf("stored refresh_token = %q, want %q", string(refreshToken), "test-rt")
	}

	mu.Lock()
	gotGrantCount := grantCount
	mu.Unlock()
	if gotGrantCount != 1 {
		t.Fatalf("grant count = %d, want 1", gotGrantCount)
	}
}

func TestRunAuthAccountFlag_APIKey(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "weather_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.weather.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	stdin := strings.NewReader("sk-prod-key\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "prod", "")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	// Verify stored under accounts path.
	val, err := store.Get(context.Background(), credpath.APIKey(testModuleString("my-package"), "weather_api", "prod"))
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(val) != "sk-prod-key" {
		t.Fatalf("stored api_key = %q, want %q", string(val), "sk-prod-key")
	}
}

func TestRunAuthAccountFlag_PathTraversal(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("my-package"),
			Name:    "my-package",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "test_api",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"example.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader("key\n"), &stdout, &stderr, "../evil", "")
	if err == nil {
		t.Fatal("expected error for path traversal account name")
	}
	if !strings.Contains(err.Error(), "invalid account name") {
		t.Fatalf("error = %v, want 'invalid account name'", err)
	}
}

func TestRunAuthCredentialFlag_FiltersToOne(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("suite"),
			Name:    "suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{
					Name: "google_workspace",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"*.googleapis.com"},
						Method: "api_key_header",
					},
				},
				{
					Name: "slack",
					Type: "api_key",
					Inject: tooldef.PackageInject{
						Hosts:  []string{"api.slack.com"},
						Method: "api_key_header",
					},
				},
			},
		},
	}

	// Only provide one line of input — should only prompt for the filtered credential.
	stdin := strings.NewReader("gws-key\n")
	var stdout, stderr bytes.Buffer

	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "", "google_workspace")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	// google_workspace stored under accounts/default/, slack not stored.
	val, err := store.Get(context.Background(), credpath.APIKey(testModuleString("suite"), "google_workspace", "default"))
	if err != nil {
		t.Fatalf("Get(google_workspace) error: %v", err)
	}
	if string(val) != "gws-key" {
		t.Fatalf("google_workspace api_key = %q, want gws-key", string(val))
	}

	_, err = store.Get(context.Background(), credpath.Shared(testModuleString("suite"), "slack", "api_key"))
	if err == nil {
		t.Fatal("slack should not have been stored")
	}
}

func TestRunAuthCredentialFlag_UnknownName(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("suite"),
			Name:    "suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{Name: "google_workspace", Type: "api_key", Inject: tooldef.PackageInject{Hosts: []string{"example.com"}, Method: "api_key_header"}},
			},
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader(""), &stdout, &stderr, "", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown credential name")
	}
	if !strings.Contains(err.Error(), "not found in package") {
		t.Fatalf("error = %v, want 'not found in package'", err)
	}
}

func TestRunAuthAccountWithoutCredential_MultiCred_Errors(t *testing.T) {
	repo, _ := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("suite"),
			Name:    "suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{Name: "gws", Type: "api_key", Inject: tooldef.PackageInject{Hosts: []string{"example.com"}, Method: "api_key_header"}},
				{Name: "slack", Type: "api_key", Inject: tooldef.PackageInject{Hosts: []string{"slack.com"}, Method: "api_key_header"}},
			},
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, strings.NewReader(""), &stdout, &stderr, "admin@acme.com", "")
	if err == nil {
		t.Fatal("expected error for --account without --credential on multi-cred package")
	}
	if !strings.Contains(err.Error(), "Use --credential") {
		t.Fatalf("error = %v, want 'Use --credential'", err)
	}
}

func TestRunAuthAccountWithCredential_MultiCred_Works(t *testing.T) {
	repo, store := newTestCredentialRepo(t)

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("suite"),
			Name:    "suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{
				{Name: "gws", Type: "api_key", Inject: tooldef.PackageInject{Hosts: []string{"example.com"}, Method: "api_key_header"}},
				{Name: "slack", Type: "api_key", Inject: tooldef.PackageInject{Hosts: []string{"slack.com"}, Method: "api_key_header"}},
			},
		},
	}

	stdin := strings.NewReader("gws-admin-key\n")
	var stdout, stderr bytes.Buffer
	err := runAuthWithRepo(loaded, repo, stdin, &stdout, &stderr, "admin@acme.com", "gws")
	if err != nil {
		t.Fatalf("runAuthWithRepo() error: %v", err)
	}

	val, err := store.Get(context.Background(), credpath.APIKey(testModuleString("suite"), "gws", "admin@acme.com"))
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if string(val) != "gws-admin-key" {
		t.Fatalf("stored api_key = %q, want gws-admin-key", string(val))
	}
}

func TestDeleteAccountWithRepoRemovesOAuth2AccountSecrets(t *testing.T) {
	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-suite"),
			Name:    "oauth-suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  "https://example.com/auth",
					TokenURL: "https://example.com/token",
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	if err := store.Set(ctx, credpath.OAuth2ClientID(testModuleString("oauth-suite"), "test_oauth"), []byte("cid")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2ClientSecret(testModuleString("oauth-suite"), "test_oauth"), []byte("csec")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "work"), []byte("rt-work")); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "personal"), []byte("rt-personal")); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := deleteAccountWithRepo(loaded, repo, "test_oauth", "work", &stdout)
	if err != nil {
		t.Fatalf("deleteAccountWithRepo() error: %v", err)
	}
	if !strings.Contains(stdout.String(), `Deleted account "work" for credential test_oauth (1 secrets removed)`) {
		t.Fatalf("stdout = %q, want delete confirmation", stdout.String())
	}

	_, err = store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "work"))
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(work refresh_token) error = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(ctx, credpath.OAuth2ClientID(testModuleString("oauth-suite"), "test_oauth")); err != nil {
		t.Fatalf("Get(client_id) error: %v", err)
	}
	if _, err := store.Get(ctx, credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "personal")); err != nil {
		t.Fatalf("Get(personal refresh_token) error: %v", err)
	}
}

func TestDeleteCredentialWithRepoRemovesOAuth2SharedAndAccountSecrets(t *testing.T) {
	repo, store := newTestCredentialRepo(t)
	ctx := context.Background()

	loaded := packaging.LoadedPackage{
		Package: tooldef.Package{
			Module:  testModule("oauth-suite"),
			Name:    "oauth-suite",
			Runtime: tooldef.RuntimeTypeScriptSandbox,
			Credentials: []tooldef.PackageCredential{{
				Name: "test_oauth",
				Type: "oauth2",
				Provider: &tooldef.OAuth2ProviderConfig{
					AuthURL:  "https://example.com/auth",
					TokenURL: "https://example.com/token",
				},
				Inject: tooldef.PackageInject{
					Hosts:  []string{"api.example.com"},
					Method: "bearer_header",
				},
			}},
		},
	}

	keys := map[string][]byte{
		credpath.OAuth2ClientID(testModuleString("oauth-suite"), "test_oauth"):                 []byte("cid"),
		credpath.OAuth2ClientSecret(testModuleString("oauth-suite"), "test_oauth"):             []byte("csec"),
		credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "work"):     []byte("rt-work"),
		credpath.OAuth2RefreshToken(testModuleString("oauth-suite"), "test_oauth", "personal"): []byte("rt-personal"),
	}
	for key, value := range keys {
		if err := store.Set(ctx, key, value); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}

	var stdout bytes.Buffer
	err := deleteCredentialWithRepo(loaded, repo, "test_oauth", &stdout)
	if err != nil {
		t.Fatalf("deleteCredentialWithRepo() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted credential test_oauth (4 secrets removed)") {
		t.Fatalf("stdout = %q, want delete confirmation", stdout.String())
	}

	for key := range keys {
		_, err := store.Get(ctx, key)
		if !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("Get(%q) error = %v, want ErrNotFound", key, err)
		}
	}
}

func testModule(name string) tooldef.ModulePath {
	return tooldef.ModulePath(testModuleString(name))
}

func testModuleString(name string) string {
	return "example.com/" + name
}
