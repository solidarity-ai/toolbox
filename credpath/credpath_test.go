package credpath

import "testing"

func TestShared(t *testing.T) {
	t.Parallel()
	tests := []struct {
		module, cred, suffix, want string
	}{
		{"google-workspace", "gws", "client_id", "google-workspace/gws/client_id"},
		{"google-workspace", "gws", "client_secret", "google-workspace/gws/client_secret"},
		{"maps", "default", "api_key", "maps/default/api_key"},
	}
	for _, tt := range tests {
		got := Shared(tt.module, tt.cred, tt.suffix)
		if got != tt.want {
			t.Errorf("Shared(%q, %q, %q) = %q, want %q", tt.module, tt.cred, tt.suffix, got, tt.want)
		}
	}
}

func TestAccount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		module, cred, account, suffix, want string
	}{
		{"google-workspace", "gws", "admin", "refresh_token", "google-workspace/gws/accounts/admin/refresh_token"},
		{"my-package", "weather_api", "default", "api_key", "my-package/weather_api/accounts/default/api_key"},
		{"pkg", "gws", "admin@acme.com", "refresh_token", "pkg/gws/accounts/admin@acme.com/refresh_token"},
	}
	for _, tt := range tests {
		got := Account(tt.module, tt.cred, tt.account, tt.suffix)
		if got != tt.want {
			t.Errorf("Account(%q, %q, %q, %q) = %q, want %q", tt.module, tt.cred, tt.account, tt.suffix, got, tt.want)
		}
	}
}

func TestAccountPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		module, cred, account, want string
	}{
		{"google-workspace", "gws", "admin", "google-workspace/gws/accounts/admin/"},
		{"pkg", "gws", "admin@acme.com", "pkg/gws/accounts/admin@acme.com/"},
	}
	for _, tt := range tests {
		got := AccountPrefix(tt.module, tt.cred, tt.account)
		if got != tt.want {
			t.Errorf("AccountPrefix(%q, %q, %q) = %q, want %q", tt.module, tt.cred, tt.account, got, tt.want)
		}
	}
}

func TestSharedPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		module, cred, want string
	}{
		{"google-workspace", "gws", "google-workspace/gws/"},
		{"maps", "default", "maps/default/"},
	}
	for _, tt := range tests {
		got := SharedPrefix(tt.module, tt.cred)
		if got != tt.want {
			t.Errorf("SharedPrefix(%q, %q) = %q, want %q", tt.module, tt.cred, got, tt.want)
		}
	}
}

func TestAccountsPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		module, cred, want string
	}{
		{"google-workspace", "gws", "google-workspace/gws/accounts/"},
		{"mod", "weather", "mod/weather/accounts/"},
	}
	for _, tt := range tests {
		got := AccountsPrefix(tt.module, tt.cred)
		if got != tt.want {
			t.Errorf("AccountsPrefix(%q, %q) = %q, want %q", tt.module, tt.cred, got, tt.want)
		}
	}
}

func TestOAuth2Helpers(t *testing.T) {
	t.Parallel()

	if got, want := OAuth2ClientID("gw", "cred"), "gw/cred/client_id"; got != want {
		t.Errorf("OAuth2ClientID = %q, want %q", got, want)
	}
	if got, want := OAuth2ClientSecret("gw", "cred"), "gw/cred/client_secret"; got != want {
		t.Errorf("OAuth2ClientSecret = %q, want %q", got, want)
	}
	if got, want := OAuth2RefreshToken("gw", "cred", "admin"), "gw/cred/accounts/admin/refresh_token"; got != want {
		t.Errorf("OAuth2RefreshToken = %q, want %q", got, want)
	}
}

func TestAPIKeyHelper(t *testing.T) {
	t.Parallel()

	if got, want := APIKey("pkg", "weather", "default"), "pkg/weather/accounts/default/api_key"; got != want {
		t.Errorf("APIKey = %q, want %q", got, want)
	}
}

func TestBearerHelpers(t *testing.T) {
	t.Parallel()

	if got, want := BearerToken("pkg", "gh", "default"), "pkg/gh/accounts/default/token"; got != want {
		t.Errorf("BearerToken = %q, want %q", got, want)
	}
	if got, want := BearerUsername("pkg", "jira", "admin"), "pkg/jira/accounts/admin/username"; got != want {
		t.Errorf("BearerUsername = %q, want %q", got, want)
	}
	if got, want := BearerPassword("pkg", "jira", "admin"), "pkg/jira/accounts/admin/password"; got != want {
		t.Errorf("BearerPassword = %q, want %q", got, want)
	}
}
