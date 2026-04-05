package invoke

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestBuildInjectionRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pkg        tooldef.Package
		moduleName string
		want       []transport.InjectionRule
	}{
		{
			name: "oauth2 with known provider",
			pkg: tooldef.Package{
				Name: "google-workspace",
				Credentials: []tooldef.PackageCredential{
					{
						Name:     "default",
						Type:     "oauth2",
						Provider: &tooldef.OAuth2ProviderConfig{Name: "google"},
						Scopes:   []string{"https://www.googleapis.com/auth/admin.directory.user.readonly"},
						Inject: tooldef.PackageInject{
							Hosts:      []string{"*.googleapis.com"},
							Method:     "bearer_header",
							PathPrefix: "/admin/directory/v1/",
						},
					},
				},
			},
			moduleName: "google-workspace",
			want: []transport.InjectionRule{
				{
					Hosts:          []string{"*.googleapis.com"},
					PathPrefix:     "/admin/directory/v1/",
					ModuleName:     "google-workspace",
					CredentialName: "default",
					SecretPrefix:   credpath.SharedPrefix("google-workspace", "default"),
					Type:           transport.CredentialTypeOAuth2,
					Method:         transport.InjectionMethodBearerHeader,
					Provider: &transport.OAuth2Provider{
						AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
						TokenURL: "https://oauth2.googleapis.com/token",
					},
				},
			},
		},
		{
			name: "api_key with header injection",
			pkg: tooldef.Package{
				Name: "maps",
				Credentials: []tooldef.PackageCredential{
					{
						Name: "default",
						Type: "api_key",
						Inject: tooldef.PackageInject{
							Hosts:  []string{"maps.googleapis.com"},
							Method: "api_key_header",
						},
					},
				},
			},
			moduleName: "maps",
			want: []transport.InjectionRule{
				{
					Hosts:          []string{"maps.googleapis.com"},
					ModuleName:     "maps",
					CredentialName: "default",
					SecretPrefix:   credpath.SharedPrefix("maps", "default"),
					Type:           transport.CredentialTypeAPIKey,
					Method:         transport.InjectionMethodAPIKeyHeader,
				},
			},
		},
		{
			name: "custom provider URLs",
			pkg: tooldef.Package{
				Name: "custom-service",
				Credentials: []tooldef.PackageCredential{
					{
						Name: "default",
						Type: "oauth2",
						Provider: &tooldef.OAuth2ProviderConfig{
							AuthURL:  "https://auth.example.com/authorize",
							TokenURL: "https://auth.example.com/token",
						},
						Inject: tooldef.PackageInject{
							Hosts:  []string{"api.example.com"},
							Method: "bearer_header",
						},
					},
				},
			},
			moduleName: "custom-service",
			want: []transport.InjectionRule{
				{
					Hosts:          []string{"api.example.com"},
					ModuleName:     "custom-service",
					CredentialName: "default",
					SecretPrefix:   credpath.SharedPrefix("custom-service", "default"),
					Type:           transport.CredentialTypeOAuth2,
					Method:         transport.InjectionMethodBearerHeader,
					Provider: &transport.OAuth2Provider{
						AuthURL:  "https://auth.example.com/authorize",
						TokenURL: "https://auth.example.com/token",
					},
				},
			},
		},
		{
			name: "api_key with custom header name",
			pkg: tooldef.Package{
				Name: "weather",
				Credentials: []tooldef.PackageCredential{
					{
						Name: "default",
						Type: "api_key",
						Inject: tooldef.PackageInject{
							Hosts:                    []string{"api.weather.com"},
							Method:                   "api_key_header",
							HeaderName:               "Api-Key",
							AllowUnsafeHTTPInjection: true,
						},
					},
				},
			},
			moduleName: "weather",
			want: []transport.InjectionRule{
				{
					Hosts:                    []string{"api.weather.com"},
					ModuleName:               "weather",
					CredentialName:           "default",
					SecretPrefix:             credpath.SharedPrefix("weather", "default"),
					Type:                     transport.CredentialTypeAPIKey,
					Method:                   transport.InjectionMethodAPIKeyHeader,
					HeaderName:               "Api-Key",
					AllowUnsafeHTTPInjection: true,
				},
			},
		},
		{
			name: "no credentials produces empty rules",
			pkg: tooldef.Package{
				Name: "calc",
			},
			moduleName: "calc",
			want:       []transport.InjectionRule{},
		},
		{
			name: "unknown provider name produces nil provider",
			pkg: tooldef.Package{
				Name: "unknown",
				Credentials: []tooldef.PackageCredential{
					{
						Name:     "default",
						Type:     "oauth2",
						Provider: &tooldef.OAuth2ProviderConfig{Name: "unknown-provider"},
						Inject: tooldef.PackageInject{
							Hosts:  []string{"api.unknown.com"},
							Method: "bearer_header",
						},
					},
				},
			},
			moduleName: "unknown",
			want: []transport.InjectionRule{
				{
					Hosts:          []string{"api.unknown.com"},
					ModuleName:     "unknown",
					CredentialName: "default",
					SecretPrefix:   credpath.SharedPrefix("unknown", "default"),
					Type:           transport.CredentialTypeOAuth2,
					Method:         transport.InjectionMethodBearerHeader,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.pkg.Module = tooldef.ModulePath(tt.moduleName)
			got := credentialrepo.BuildInjectionRules(tt.pkg)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("BuildInjectionRules() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverCredentialAccounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		seeds      map[string][]byte
		pkg        tooldef.Package
		moduleName string
		want       map[string][]string
		wantErr    string
	}{
		{
			name: "two named accounts",
			seeds: map[string][]byte{
				credpath.OAuth2RefreshToken("mod", "gws", "admin@acme.com"):       []byte("rt1"),
				credpath.Account("mod", "gws", "admin@acme.com", "client_id"):     []byte("cid"),
				credpath.OAuth2RefreshToken("mod", "gws", "personal@gmail.com"):   []byte("rt2"),
				credpath.Account("mod", "gws", "personal@gmail.com", "client_id"): []byte("cid"),
			},
			pkg: tooldef.Package{
				Credentials: []tooldef.PackageCredential{{Name: "gws", Type: "oauth2"}},
			},
			moduleName: "mod",
			want:       map[string][]string{"gws": {"admin@acme.com", "personal@gmail.com"}},
		},
		{
			name:  "no keys at all",
			seeds: map[string][]byte{},
			pkg: tooldef.Package{
				Credentials: []tooldef.PackageCredential{{Name: "gws", Type: "oauth2"}},
			},
			moduleName: "mod",
			want:       map[string][]string{"gws": {}},
		},
		{
			name: "single account under accounts path",
			seeds: map[string][]byte{
				credpath.OAuth2RefreshToken("mod", "gws", "default"): []byte("rt"),
				credpath.OAuth2ClientID("mod", "gws"):                []byte("cid"),
			},
			pkg: tooldef.Package{
				Credentials: []tooldef.PackageCredential{{Name: "gws", Type: "oauth2"}},
			},
			moduleName: "mod",
			want:       map[string][]string{"gws": {"default"}},
		},
		{
			name: "api_key single account under accounts path",
			seeds: map[string][]byte{
				credpath.APIKey("mod", "weather", "default"): []byte("key"),
			},
			pkg: tooldef.Package{
				Credentials: []tooldef.PackageCredential{{Name: "weather", Type: "api_key"}},
			},
			moduleName: "mod",
			want:       map[string][]string{"weather": {"default"}},
		},
		{
			name: "api_key named account",
			seeds: map[string][]byte{
				credpath.APIKey("mod", "weather", "prod"): []byte("key"),
			},
			pkg: tooldef.Package{
				Credentials: []tooldef.PackageCredential{{Name: "weather", Type: "api_key"}},
			},
			moduleName: "mod",
			want:       map[string][]string{"weather": {"prod"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := testutil.NewTestSecretStore()
			store.Seed(tt.seeds)

			tt.pkg.Module = tooldef.ModulePath(tt.moduleName)
			got, err := credentialrepo.DiscoverCredentialAccounts(context.Background(), tt.pkg, store)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("DiscoverCredentialAccounts() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
