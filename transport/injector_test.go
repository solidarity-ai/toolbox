package transport_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestInjectorExactHostBeatsWildcard(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/exact":    []byte("exact-token"),
		"pkg/wildcard": []byte("wildcard-token"),
	}},
		transport.Rule{
			Name:      "wildcard",
			SecretKey: "pkg/wildcard",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"*.github.com"},
				Method: "bearer_header",
			},
		},
		transport.Rule{
			Name:      "exact",
			SecretKey: "pkg/exact",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		},
	)

	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer exact-token"}}, headers.Entries()); diff != "" {
		t.Fatalf("headers mismatch (-want +got):\n%s", diff)
	}
}

func TestInjectorLongerPathPrefixBeatsShorterOnSameHost(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/root": []byte("root-token"),
		"pkg/deep": []byte("deep-token"),
	}},
		transport.Rule{
			Name:      "root",
			SecretKey: "pkg/root",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos",
				Method:     "bearer_header",
			},
		},
		transport.Rule{
			Name:      "deep",
			SecretKey: "pkg/deep",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos/private",
				Method:     "bearer_header",
			},
		},
	)

	headers := fetch.NewHeaders()
	_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/private/secret", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if diff := cmp.Diff([][2]string{{"authorization", "Bearer deep-token"}}, headers.Entries()); diff != "" {
		t.Fatalf("headers mismatch (-want +got):\n%s", diff)
	}
}

func TestInjectorWildcardDoesNotMatchBareParentHost(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{
		"pkg/wildcard": []byte("wildcard-token"),
	}}, transport.Rule{
		Name:      "wildcard",
		SecretKey: "pkg/wildcard",
		Type:      tooldef.CredentialTypeBearer,
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"*.googleapis.com"},
			Method: "bearer_header",
		},
	})

	headers := fetch.NewHeaders()
	gotURL, err := injector.InjectRequest(context.Background(), "https://googleapis.com/discovery/v1/apis", headers)
	if err != nil {
		t.Fatalf("InjectRequest: %v", err)
	}
	if gotURL != "https://googleapis.com/discovery/v1/apis" {
		t.Fatalf("rewritten url = %q, want original", gotURL)
	}
	if len(headers.Entries()) != 0 {
		t.Fatalf("headers = %v, want no injection", headers.Entries())
	}
}

func TestInjectorBuiltInMethods(t *testing.T) {
	t.Parallel()

	t.Run("basic_auth emits one authorization header", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/basic": []byte("octocat:secret")}}, transport.Rule{
			Name:      "basic",
			SecretKey: "pkg/basic",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "basic_auth",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("octocat:secret"))
		if diff := cmp.Diff([][2]string{{"authorization", want}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("api_key_header uses configured header name", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/header": []byte("header-token")}}, transport.Rule{
			Name:      "header",
			SecretKey: "pkg/header",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.example.com"},
				Method:     "api_key_header",
				HeaderName: "X-Custom-Key",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.example.com/data", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if diff := cmp.Diff([][2]string{{"x-custom-key", "header-token"}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("api_key_query uses configured query name", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/query": []byte("query-token")}}, transport.Rule{
			Name:      "query",
			SecretKey: "pkg/query",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject: tooldef.CredentialInject{
				Hosts:     []string{"api.example.com"},
				Method:    "api_key_query",
				QueryName: "api_key",
			},
		})

		headers := fetch.NewHeaders()
		gotURL, err := injector.InjectRequest(context.Background(), "https://api.example.com/data?existing=1", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if gotURL != "https://api.example.com/data?api_key=query-token&existing=1" && gotURL != "https://api.example.com/data?existing=1&api_key=query-token" {
			t.Fatalf("rewritten url = %q, want query injection", gotURL)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no header mutation", headers.Entries())
		}
	})

	t.Run("oauth2 reads reserved access_token family key", func(t *testing.T) {
		t.Parallel()
		const accessTokenKey = "github.com/example/github-issues/github_oauth/access_token"
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{accessTokenKey: []byte("oauth-token")}}, transport.Rule{
			Name:      "github_oauth",
			SecretKey: accessTokenKey,
			Type:      tooldef.CredentialTypeOAuth2,
			Inject: tooldef.CredentialInject{
				Hosts:  []string{"api.github.com"},
				Method: "bearer_header",
			},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err != nil {
			t.Fatalf("InjectRequest: %v", err)
		}
		if diff := cmp.Diff([][2]string{{"authorization", "Bearer oauth-token"}}, headers.Entries()); diff != "" {
			t.Fatalf("headers mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestInjectorRejectsMalformedInputsAtConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule transport.Rule
		want string
	}{
		{
			name: "empty host pattern",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"   "}},
			},
			want: "host pattern must not be empty",
		},
		{
			name: "unsupported method",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "digest_auth"},
			},
			want: "unsupported injection method",
		},
		{
			name: "api key header requires header name",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeAPIKey,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "api_key_header"},
			},
			want: "headerName must not be empty",
		},
		{
			name: "api key query requires query name",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeAPIKey,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "api_key_query"},
			},
			want: "queryName must not be empty",
		},
		{
			name: "malformed path prefix",
			rule: transport.Rule{
				Name:      "bad",
				SecretKey: "pkg/bad",
				Type:      tooldef.CredentialTypeBearer,
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, PathPrefix: "repos"},
			},
			want: "path prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := transport.NewInjector(nil, []transport.Rule{tt.rule})
			if err == nil {
				t.Fatal("expected NewInjector to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewInjector error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestInjectorRejectsAmbiguousCanonicalRules(t *testing.T) {
	t.Parallel()

	_, err := transport.NewInjector(nil, []transport.Rule{
		{
			Name:      "first",
			SecretKey: "pkg/first",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{" API.GITHUB.COM "},
				PathPrefix: "/repos/",
				Method:     "bearer_header",
			},
		},
		{
			Name:      "second",
			SecretKey: "pkg/second",
			Type:      tooldef.CredentialTypeBearer,
			Inject: tooldef.CredentialInject{
				Hosts:      []string{"api.github.com"},
				PathPrefix: "/repos",
				Method:     "bearer_header",
			},
		},
	})
	if err == nil {
		t.Fatal("expected NewInjector to reject ambiguous canonical rules")
	}
	if !strings.Contains(err.Error(), "ambiguous transport credential rules") {
		t.Fatalf("NewInjector error = %v, want ambiguity context", err)
	}
}

func TestInjectorReportsMissingSecretStoreAndSecretValueFailures(t *testing.T) {
	t.Parallel()

	t.Run("missing secret store", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, nil, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected missing secret store error")
		}
		if !strings.Contains(err.Error(), "no secret store") {
			t.Fatalf("InjectRequest error = %v, want missing store context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("missing secret value", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{err: secrets.ErrNotFound}, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected missing secret error")
		}
		if !strings.Contains(err.Error(), "resolve transport credential \"pkg/token\"") {
			t.Fatalf("InjectRequest error = %v, want secret lookup context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("unusable secret material", func(t *testing.T) {
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/token": []byte("   ")}}, transport.Rule{
			Name:      "token",
			SecretKey: "pkg/token",
			Type:      tooldef.CredentialTypeBearer,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/repos/octocat/hello-world", headers)
		if err == nil {
			t.Fatal("expected unusable secret material error")
		}
		if !strings.Contains(err.Error(), "resolved unusable secret material") {
			t.Fatalf("InjectRequest error = %v, want unusable secret context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})

	t.Run("basic auth rejects malformed secret material", func(t *testing.T) {
		t.Parallel()
		injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/basic": []byte("missing-colon")}}, transport.Rule{
			Name:      "basic",
			SecretKey: "pkg/basic",
			Type:      tooldef.CredentialTypeAPIKey,
			Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "basic_auth"},
		})

		headers := fetch.NewHeaders()
		_, err := injector.InjectRequest(context.Background(), "https://api.github.com/user", headers)
		if err == nil {
			t.Fatal("expected malformed basic_auth secret error")
		}
		if !strings.Contains(err.Error(), "malformed basic_auth secret material") {
			t.Fatalf("InjectRequest error = %v, want malformed basic_auth context", err)
		}
		if len(headers.Entries()) != 0 {
			t.Fatalf("headers = %v, want no mutation on error", headers.Entries())
		}
	})
}

func TestInjectorRejectsInvalidRequestURLs(t *testing.T) {
	t.Parallel()

	injector := newInjector(t, stubSecretStore{values: map[string][]byte{"pkg/token": []byte("token")}}, transport.Rule{
		Name:      "token",
		SecretKey: "pkg/token",
		Type:      tooldef.CredentialTypeBearer,
		Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "bearer_header"},
	})

	_, err := injector.InjectRequest(context.Background(), "://bad-url", fetch.NewHeaders())
	if err == nil {
		t.Fatal("expected invalid url error")
	}
	if !strings.Contains(err.Error(), "parse request url") {
		t.Fatalf("InjectRequest error = %v, want parse context", err)
	}
}

func TestNormalizeAllowedHosts(t *testing.T) {
	t.Parallel()

	got := transport.NormalizeAllowedHosts([]string{" *.googleapis.com ", "oauth2.googleapis.com", "oauth2.googleapis.com", ""})
	want := []string{"*.googleapis.com", "oauth2.googleapis.com"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("NormalizeAllowedHosts mismatch (-want +got):\n%s", diff)
	}
}

func newInjector(t testing.TB, store secrets.SecretStore, rules ...transport.Rule) *transport.Injector {
	t.Helper()
	injector, err := transport.NewInjector(store, rules)
	if err != nil {
		t.Fatalf("NewInjector: %v", err)
	}
	return injector
}

type stubSecretStore struct {
	values map[string][]byte
	err    error
}

func (s stubSecretStore) Get(_ context.Context, key string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return nil, secrets.ErrNotFound
}

func (s stubSecretStore) Set(context.Context, string, []byte) error {
	return errors.New("not implemented")
}

func (s stubSecretStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func (s stubSecretStore) List(context.Context, string) ([]string, error) {
	return nil, errors.New("not implemented")
}
