package transport_test

import (
	"context"
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
				Inject:    tooldef.CredentialInject{Hosts: []string{"api.github.com"}, Method: "basic_auth"},
			},
			want: "unsupported injection method",
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
		t.Parallel()
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
