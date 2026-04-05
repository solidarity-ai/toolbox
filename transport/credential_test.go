package transport_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/transport"
)

// mockTokenServer starts an httptest server that responds to OAuth2 token
// requests, returning the given accessToken. It increments *calls on each
// request so tests can verify caching / singleflight behaviour.
func mockTokenServer(t *testing.T, accessToken string, expiresIn int, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedOAuth2Secrets(t *testing.T, store *testutil.TestSecretStore, moduleName, credName string) {
	t.Helper()
	store.Seed(map[string][]byte{
		credpath.OAuth2ClientID(moduleName, credName):          []byte("test-client-id"),
		credpath.OAuth2ClientSecret(moduleName, credName):      []byte("test-client-secret"),
		credpath.Shared(moduleName, credName, "refresh_token"): []byte("test-refresh-token"),
	})
}

func TestCredentialInjector_BearerInjection(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "my-secret-token", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/admin/directory/v1/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	url, headers, injected, err := ci.InjectRequest("GET", "https://admin.googleapis.com/admin/directory/v1/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	// Upstream must receive the Authorization header.
	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer my-secret-token" {
		t.Fatalf("Authorization header = %q, want %q", authHeader, "Bearer my-secret-token")
	}

	// The returned URL must NOT contain the token.
	if strings.Contains(url, "my-secret-token") {
		t.Fatalf("returned URL %q leaks the token", url)
	}

	if c := calls.Load(); c != 1 {
		t.Fatalf("token endpoint called %d times, want 1", c)
	}
}

func TestCredentialInjector_NoMatchingRule(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/admin/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, headers, injected, err := ci.InjectRequest("GET", "https://api.slack.com/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if injected {
		t.Fatal("expected injected=false for non-matching host")
	}
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			t.Fatalf("unexpected Authorization header on non-matching request")
		}
	}
}

func TestCredentialInjector_DenyListPaths(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "secret", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	denyPaths := []string{
		"https://accounts.googleapis.com/oauth2/token",
		"https://accounts.googleapis.com/oauth2/v4/token",
		"https://accounts.googleapis.com/token",
		"https://oauth2.googleapis.com/token",
	}
	for _, u := range denyPaths {
		_, _, injected, err := ci.InjectRequest("POST", u, nil)
		if err != nil {
			t.Fatalf("InjectRequest(%s): %v", u, err)
		}
		if injected {
			t.Errorf("credentials should NOT be injected for deny-listed path %s", u)
		}
	}
}

func TestCredentialInjector_WildcardHostMatching(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "tok", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	tests := []struct {
		host  string
		match bool
	}{
		{"www.googleapis.com", true},
		{"admin.googleapis.com", true},
		{"storage.googleapis.com", true},
		{"googleapis.com", false},              // wildcard requires a subdomain
		{"evil.googleapis.com.bad.com", false}, // must not match suffix
	}
	for _, tt := range tests {
		url := "https://" + tt.host + "/some/path"
		_, _, injected, err := ci.InjectRequest("GET", url, nil)
		if err != nil {
			t.Fatalf("InjectRequest(%s): %v", tt.host, err)
		}
		if injected != tt.match {
			t.Errorf("host %s: injected=%v, want %v", tt.host, injected, tt.match)
		}
	}
}

func TestCredentialInjector_ExactHostMatching(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "tok", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "slack", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"api.slack.com"},
			PathPrefix:     "/",
			ModuleName:     "slack",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("slack", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	tests := []struct {
		host  string
		match bool
	}{
		{"api.slack.com", true},
		{"slack.com", false},
		{"evil.api.slack.com", false},
		{"api.slack.com.evil.com", false},
	}
	for _, tt := range tests {
		url := "https://" + tt.host + "/api/users"
		_, _, injected, err := ci.InjectRequest("GET", url, nil)
		if err != nil {
			t.Fatalf("InjectRequest(%s): %v", tt.host, err)
		}
		if injected != tt.match {
			t.Errorf("host %s: injected=%v, want %v", tt.host, injected, tt.match)
		}
	}
}

func TestCredentialInjector_PathPrefixMatching(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "tok", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/admin/directory/v1/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	tests := []struct {
		path  string
		match bool
	}{
		{"/admin/directory/v1/users", true},
		{"/admin/directory/v1/groups/abc", true},
		{"/admin/directory/v1/", true},
		{"/calendar/v3/events", false},
		{"/admin/reports/v1/usage", false},
		{"/admin/directory/v2/users", false},
	}
	for _, tt := range tests {
		url := "https://admin.googleapis.com" + tt.path
		_, _, injected, err := ci.InjectRequest("GET", url, nil)
		if err != nil {
			t.Fatalf("InjectRequest(%s): %v", tt.path, err)
		}
		if injected != tt.match {
			t.Errorf("path %s: injected=%v, want %v", tt.path, injected, tt.match)
		}
	}
}

func TestCredentialInjector_APIKeyHeaderInjection(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("maps", "default", "api_key"): []byte("MAPS-API-KEY-123"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"maps.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "maps",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("maps", "default"),
			Type:           transport.CredentialTypeAPIKey,
			Method:         transport.InjectionMethodAPIKeyHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, headers, injected, err := ci.InjectRequest("GET", "https://maps.googleapis.com/maps/api/geocode", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	var apiKeyHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "X-API-Key") {
			apiKeyHeader = h[1]
		}
	}
	if apiKeyHeader != "MAPS-API-KEY-123" {
		t.Fatalf("X-API-Key header = %q, want %q", apiKeyHeader, "MAPS-API-KEY-123")
	}
}

func TestCredentialInjector_APIKeyQueryInjection(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("maps", "default", "api_key"): []byte("MAPS-API-KEY-456"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"maps.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "maps",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("maps", "default"),
			Type:           transport.CredentialTypeAPIKey,
			Method:         transport.InjectionMethodAPIKeyQuery,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	url, _, injected, err := ci.InjectRequest("GET", "https://maps.googleapis.com/maps/api/geocode", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	if !strings.Contains(url, "key=MAPS-API-KEY-456") {
		t.Fatalf("returned URL %q does not contain key= query param", url)
	}

	// Verify existing query params are preserved.
	url2, _, injected2, err := ci.InjectRequest("GET", "https://maps.googleapis.com/maps/api/geocode?existing=param", nil)
	if err != nil {
		t.Fatalf("InjectRequest with existing params: %v", err)
	}
	if !injected2 {
		t.Fatal("expected injected=true with existing params")
	}
	if !strings.Contains(url2, "existing=param") {
		t.Fatalf("returned URL %q lost existing query param", url2)
	}
	if !strings.Contains(url2, "key=MAPS-API-KEY-456") {
		t.Fatalf("returned URL %q does not contain key= query param", url2)
	}
}

func TestCredentialInjector_BasicAuthInjection(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("jira", "default", "username"): []byte("admin"),
		credpath.Shared("jira", "default", "password"): []byte("hunter2"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"jira.example.com"},
			PathPrefix:     "/",
			ModuleName:     "jira",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("jira", "default"),
			Type:           transport.CredentialTypeBearer,
			Method:         transport.InjectionMethodBasicAuth,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, headers, injected, err := ci.InjectRequest("GET", "https://jira.example.com/rest/api/2/issue/PROJ-1", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:hunter2"))
	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != expected {
		t.Fatalf("Authorization header = %q, want %q", authHeader, expected)
	}
}

func TestCredentialInjector_TokenCacheHit(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "cached-token", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	// First call: should hit the token endpoint.
	_, _, injected, err := ci.InjectRequest("GET", "https://admin.googleapis.com/admin/v1/users", nil)
	if err != nil {
		t.Fatalf("first InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("first call: expected injected=true")
	}

	// Second call: should use cached token, not hit endpoint again.
	_, headers, injected, err := ci.InjectRequest("GET", "https://storage.googleapis.com/bucket/object", nil)
	if err != nil {
		t.Fatalf("second InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("second call: expected injected=true")
	}

	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer cached-token" {
		t.Fatalf("Authorization = %q, want Bearer cached-token", authHeader)
	}

	if c := calls.Load(); c != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cache miss only)", c)
	}
}

func TestCredentialInjector_TokenRefreshOnExpiry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	// Token expires in 1 second.
	tokenSrv := mockTokenServer(t, "refreshed-token", 1, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	// First call: fetch token.
	_, _, _, err := ci.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("first InjectRequest: %v", err)
	}
	if c := calls.Load(); c != 1 {
		t.Fatalf("after first call: token endpoint called %d times, want 1", c)
	}

	// Wait for token to expire.
	time.Sleep(2 * time.Second)

	// Second call: token expired, should refresh.
	_, headers, injected, err := ci.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("second InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("second call: expected injected=true")
	}

	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer refreshed-token" {
		t.Fatalf("Authorization = %q, want Bearer refreshed-token", authHeader)
	}

	if c := calls.Load(); c != 2 {
		t.Fatalf("token endpoint called %d times, want 2 (initial + refresh)", c)
	}
}

func TestCredentialInjector_PersistsRotatedRefreshToken(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.FormValue("refresh_token") {
		case "test-refresh-token":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "first-access-token",
				"refresh_token": "rotated-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    1,
			})
		case "rotated-refresh-token":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "second-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"error":             "invalid_grant",
				"error_description": "refresh token is no longer valid",
			})
		}
	}))
	t.Cleanup(tokenSrv.Close)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	_, headers, injected, err := ci.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("first InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("first call: expected injected=true")
	}

	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer first-access-token" {
		t.Fatalf("first Authorization = %q, want Bearer first-access-token", authHeader)
	}

	refreshToken, err := store.Get(context.Background(), credpath.Shared("google", "default", "refresh_token"))
	if err != nil {
		t.Fatalf("Get(refresh_token): %v", err)
	}
	if string(refreshToken) != "rotated-refresh-token" {
		t.Fatalf("stored refresh_token = %q, want %q", string(refreshToken), "rotated-refresh-token")
	}

	_, headers, injected, err = ci.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("second InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("second call: expected injected=true")
	}

	authHeader = ""
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer second-access-token" {
		t.Fatalf("second Authorization = %q, want Bearer second-access-token", authHeader)
	}

	if c := calls.Load(); c != 2 {
		t.Fatalf("token endpoint called %d times, want 2", c)
	}
}

func TestCredentialInjector_ConcurrentRefresh(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "concurrent-token", 3600, &calls)

	store := testutil.NewTestSecretStore()
	seedOAuth2Secrets(t, store, "google", "default")

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "google",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("google", "default"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider: &transport.OAuth2Provider{
				TokenURL: tokenSrv.URL + "/token",
			},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, headers, injected, err := ci.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
			if err != nil {
				errs <- err
				return
			}
			if !injected {
				errs <- &concurrentError{"expected injected=true"}
				return
			}
			var found bool
			for _, h := range headers {
				if strings.EqualFold(h[0], "Authorization") && h[1] == "Bearer concurrent-token" {
					found = true
				}
			}
			if !found {
				errs <- &concurrentError{"missing or wrong Authorization header"}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("goroutine error: %v", err)
	}

	// Singleflight: only one token refresh should have happened.
	if c := calls.Load(); c != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (singleflight)", c)
	}
}

func TestValidateAccountString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account string
		wantErr bool
	}{
		{"valid email", "admin@acme.com", false},
		{"valid simple", "personal", false},
		{"empty", "", true},
		{"path traversal dotdot", "../evil", true},
		{"path traversal slash", "foo/bar", true},
		{"null byte", "foo\x00bar", true},
		{"too long", strings.Repeat("a", 257), true},
		{"exactly 256", strings.Repeat("a", 256), false},
		{"dotdot in middle", "foo..bar", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := transport.ValidateAccountString(tt.account)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAccountString(%q) error = %v, wantErr = %v", tt.account, err, tt.wantErr)
			}
		})
	}
}

func TestWithAccounts_PathTraversalRejection(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	ci := transport.NewCredentialInjector(nil, store)

	bad := []string{"../evil", "foo/bar", "foo\x00bar", "", strings.Repeat("a", 257)}
	for _, acct := range bad {
		_, err := ci.WithAccounts(map[string]string{"cred": acct})
		if err == nil {
			t.Errorf("WithAccounts with account %q should have failed", acct)
		}
	}
}

func TestWithAccounts_ScopesSecretPrefix(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	tokenSrv := mockTokenServer(t, "admin-token", 3600, &calls)

	store := testutil.NewTestSecretStore()
	// Seed client creds at shared prefix, refresh_token at account-scoped prefix.
	store.Seed(map[string][]byte{
		credpath.OAuth2ClientID("pkg", "gws"):                       []byte("cid"),
		credpath.OAuth2ClientSecret("pkg", "gws"):                   []byte("csec"),
		credpath.OAuth2RefreshToken("pkg", "gws", "admin@acme.com"): []byte("rt"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "pkg",
			CredentialName: "gws",
			SecretPrefix:   credpath.SharedPrefix("pkg", "gws"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider:       &transport.OAuth2Provider{TokenURL: tokenSrv.URL + "/token"},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	scoped, err := ci.WithAccounts(map[string]string{"gws": "admin@acme.com"})
	if err != nil {
		t.Fatalf("WithAccounts: %v", err)
	}

	_, headers, injected, err := scoped.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer admin-token" {
		t.Fatalf("Authorization = %q, want Bearer admin-token", authHeader)
	}
}

func TestWithAccounts_NonMatchingCredentialUnchanged(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	// Seed slack secrets at the default (non-account) prefix.
	store.Seed(map[string][]byte{
		credpath.Shared("pkg", "slack", "api_key"): []byte("SLACK-KEY"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			ModuleName:     "pkg",
			CredentialName: "gws",
			SecretPrefix:   credpath.SharedPrefix("pkg", "gws"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
		},
		{
			Hosts:          []string{"api.slack.com"},
			ModuleName:     "pkg",
			CredentialName: "slack",
			SecretPrefix:   credpath.SharedPrefix("pkg", "slack"),
			Type:           transport.CredentialTypeAPIKey,
			Method:         transport.InjectionMethodAPIKeyHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	// Only scope gws — slack should remain at default prefix.
	scoped, err := ci.WithAccounts(map[string]string{"gws": "admin@acme.com"})
	if err != nil {
		t.Fatalf("WithAccounts: %v", err)
	}

	// Slack should still resolve from unscoped prefix.
	_, headers, injected, err := scoped.InjectRequest("GET", "https://api.slack.com/api/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest (slack): %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true for slack")
	}

	var apiKey string
	for _, h := range headers {
		if strings.EqualFold(h[0], "X-API-Key") {
			apiKey = h[1]
		}
	}
	if apiKey != "SLACK-KEY" {
		t.Fatalf("X-API-Key = %q, want SLACK-KEY", apiKey)
	}
}

func TestWithAccounts_TokenCacheIsolation(t *testing.T) {
	t.Parallel()

	// Two accounts should get different tokens via separate refresh calls.
	var callCount atomic.Int64
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		r.ParseForm()
		rt := r.FormValue("refresh_token")
		// Return a token that encodes which refresh token was used.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-for-" + rt,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.OAuth2ClientID("pkg", "gws"):                           []byte("cid"),
		credpath.OAuth2ClientSecret("pkg", "gws"):                       []byte("csec"),
		credpath.OAuth2RefreshToken("pkg", "gws", "admin@acme.com"):     []byte("rt-admin"),
		credpath.OAuth2RefreshToken("pkg", "gws", "personal@gmail.com"): []byte("rt-personal"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"*.googleapis.com"},
			PathPrefix:     "/",
			ModuleName:     "pkg",
			CredentialName: "gws",
			SecretPrefix:   credpath.SharedPrefix("pkg", "gws"),
			Type:           transport.CredentialTypeOAuth2,
			Method:         transport.InjectionMethodBearerHeader,
			Provider:       &transport.OAuth2Provider{TokenURL: tokenSrv.URL + "/token"},
		},
	}

	ci := transport.NewCredentialInjector(rules, store)

	// Scope to admin account.
	adminCI, err := ci.WithAccounts(map[string]string{"gws": "admin@acme.com"})
	if err != nil {
		t.Fatalf("WithAccounts(admin): %v", err)
	}
	_, adminHeaders, _, err := adminCI.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest(admin): %v", err)
	}

	// Scope to personal account.
	personalCI, err := ci.WithAccounts(map[string]string{"gws": "personal@gmail.com"})
	if err != nil {
		t.Fatalf("WithAccounts(personal): %v", err)
	}
	_, personalHeaders, _, err := personalCI.InjectRequest("GET", "https://admin.googleapis.com/v1/users", nil)
	if err != nil {
		t.Fatalf("InjectRequest(personal): %v", err)
	}

	getAuth := func(headers [][2]string) string {
		for _, h := range headers {
			if strings.EqualFold(h[0], "Authorization") {
				return h[1]
			}
		}
		return ""
	}

	adminAuth := getAuth(adminHeaders)
	personalAuth := getAuth(personalHeaders)

	if adminAuth != "Bearer tok-for-rt-admin" {
		t.Fatalf("admin auth = %q, want Bearer tok-for-rt-admin", adminAuth)
	}
	if personalAuth != "Bearer tok-for-rt-personal" {
		t.Fatalf("personal auth = %q, want Bearer tok-for-rt-personal", personalAuth)
	}

	// Both should have triggered separate refresh calls.
	if c := callCount.Load(); c != 2 {
		t.Fatalf("token endpoint called %d times, want 2 (one per account)", c)
	}
}

func TestCredentialInjector_APIKeyHeaderCustomName(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("weather", "default", "api_key"): []byte("WEATHER-KEY"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"api.weather.com"},
			PathPrefix:     "/",
			ModuleName:     "weather",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("weather", "default"),
			Type:           transport.CredentialTypeAPIKey,
			Method:         transport.InjectionMethodAPIKeyHeader,
			HeaderName:     "Api-Key",
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, headers, injected, err := ci.InjectRequest("GET", "https://api.weather.com/forecast", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	var customHeader string
	for _, h := range headers {
		if h[0] == "Api-Key" {
			customHeader = h[1]
		}
		if h[0] == "X-API-Key" {
			t.Fatal("should not inject default X-API-Key when custom header_name is set")
		}
	}
	if customHeader != "WEATHER-KEY" {
		t.Fatalf("Api-Key header = %q, want %q", customHeader, "WEATHER-KEY")
	}
}

func TestCredentialInjector_HTTPRejectedByDefault(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.BearerToken("weather", "default", "default"): []byte("weather-token"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"api.weather.com"},
			PathPrefix:     "/",
			ModuleName:     "weather",
			CredentialName: "default",
			SecretPrefix:   credpath.AccountPrefix("weather", "default", "default"),
			Type:           transport.CredentialTypeBearer,
			Method:         transport.InjectionMethodBearerHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, _, injected, err := ci.InjectRequest("GET", "http://api.weather.com/forecast", nil)
	if err == nil {
		t.Fatal("expected error for insecure http injection, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe http") {
		t.Fatalf("error = %q, want message mentioning unsafe http", err)
	}
	if injected {
		t.Fatal("expected injected=false on insecure http rejection")
	}
}

func TestCredentialInjector_HTTPAllowedWithOptIn(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.BearerToken("weather", "default", "default"): []byte("weather-token"),
	})

	rules := []transport.InjectionRule{
		{
			Hosts:                    []string{"api.weather.com"},
			PathPrefix:               "/",
			ModuleName:               "weather",
			CredentialName:           "default",
			SecretPrefix:             credpath.AccountPrefix("weather", "default", "default"),
			Type:                     transport.CredentialTypeBearer,
			Method:                   transport.InjectionMethodBearerHeader,
			AllowUnsafeHTTPInjection: true,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	_, headers, injected, err := ci.InjectRequest("GET", "http://api.weather.com/forecast", nil)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if !injected {
		t.Fatal("expected injected=true")
	}

	var authHeader string
	for _, h := range headers {
		if strings.EqualFold(h[0], "Authorization") {
			authHeader = h[1]
		}
	}
	if authHeader != "Bearer weather-token" {
		t.Fatalf("Authorization header = %q, want %q", authHeader, "Bearer weather-token")
	}
}

func TestCredentialInjector_StripInjected_BearerHeader(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"*.googleapis.com"},
			Method: transport.InjectionMethodBearerHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/callback", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Accept", "application/json")

	ci.StripInjected(req)

	if req.Header.Get("Authorization") != "" {
		t.Fatal("StripInjected should have removed Authorization header")
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Fatal("StripInjected should not touch non-credential headers")
	}
}

func TestCredentialInjector_StripInjected_BasicAuth(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"jira.example.com"},
			Method: transport.InjectionMethodBasicAuth,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/redirect", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	ci.StripInjected(req)

	if req.Header.Get("Authorization") != "" {
		t.Fatal("StripInjected should have removed Authorization header for basic_auth")
	}
}

func TestCredentialInjector_StripInjected_APIKeyHeaderDefault(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"maps.googleapis.com"},
			Method: transport.InjectionMethodAPIKeyHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/redirect", nil)
	req.Header.Set("X-API-Key", "secret-key")

	ci.StripInjected(req)

	if req.Header.Get("X-API-Key") != "" {
		t.Fatal("StripInjected should have removed X-API-Key header")
	}
}

func TestCredentialInjector_StripInjected_APIKeyHeaderCustom(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:      []string{"api.weather.com"},
			Method:     transport.InjectionMethodAPIKeyHeader,
			HeaderName: "Api-Key",
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/redirect", nil)
	req.Header.Set("Api-Key", "secret-key")
	req.Header.Set("X-API-Key", "should-stay")

	ci.StripInjected(req)

	if req.Header.Get("Api-Key") != "" {
		t.Fatal("StripInjected should have removed custom Api-Key header")
	}
	if req.Header.Get("X-API-Key") != "should-stay" {
		t.Fatal("StripInjected should not touch X-API-Key when custom header_name is set")
	}
}

func TestCredentialInjector_StripInjected_APIKeyQuery(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"maps.googleapis.com"},
			Method: transport.InjectionMethodAPIKeyQuery,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/redirect?key=secret&other=keep", nil)

	ci.StripInjected(req)

	if req.URL.Query().Get("key") != "" {
		t.Fatal("StripInjected should have removed key query param")
	}
	if req.URL.Query().Get("other") != "keep" {
		t.Fatal("StripInjected should not touch non-credential query params")
	}
}

func TestCredentialInjector_StripInjected_MultipleRules(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"*.googleapis.com"},
			Method: transport.InjectionMethodBearerHeader,
		},
		{
			Hosts:      []string{"api.weather.com"},
			Method:     transport.InjectionMethodAPIKeyHeader,
			HeaderName: "Api-Key",
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://evil.com/redirect", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Api-Key", "key")

	ci.StripInjected(req)

	if req.Header.Get("Authorization") != "" {
		t.Fatal("StripInjected should have removed Authorization")
	}
	if req.Header.Get("Api-Key") != "" {
		t.Fatal("StripInjected should have removed Api-Key")
	}
}

func TestCredentialInjector_StripInjected_PreservedWhenDestinationMatches(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	rules := []transport.InjectionRule{
		{
			Hosts:  []string{"api.example.com"},
			Method: transport.InjectionMethodBearerHeader,
		},
	}

	ci := transport.NewCredentialInjector(rules, store)
	req, _ := http.NewRequest("GET", "https://api.example.com/v2/data", nil)
	req.Header.Set("Authorization", "Bearer my-token")

	ci.StripInjected(req)

	if req.Header.Get("Authorization") != "Bearer my-token" {
		t.Fatal("StripInjected should preserve credentials when destination matches a rule")
	}
}

func TestCredentialInjector_StripInjected_NoRules(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	ci := transport.NewCredentialInjector(nil, store)

	req, _ := http.NewRequest("GET", "https://example.com/path", nil)
	req.Header.Set("Authorization", "Bearer should-stay")

	ci.StripInjected(req)

	if req.Header.Get("Authorization") != "Bearer should-stay" {
		t.Fatal("StripInjected with no rules should not touch any headers")
	}
}

type concurrentError struct {
	msg string
}

func (e *concurrentError) Error() string { return e.msg }
