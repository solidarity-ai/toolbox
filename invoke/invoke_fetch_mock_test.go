package invoke_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestFetchMock_InterceptsViaToolsetConfig(t *testing.T) {
	t.Parallel()

	mock := tooltest.NewFetchMock().
		JSON("example.com/ping", `{"ok":true}`)

	prepared := tooltest.PrepareToolset(t,
		tooltest.DistPackageDecl("fetch-test"),
		toolset.Config{
			FetchTransport: mock,
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("fixtures.local/fetch-test"): {
					Allowlist: transport.NewHostAllowlist([]string{"*"}),
				},
			},
		})

	result, err := runInvokeString(t, prepared, "fetchTest.get", map[string]any{
		"url": "https://example.com/ping",
	})
	if err != nil {
		t.Fatalf("invoke.Run: %v", err)
	}
	if !strings.Contains(result, `ok`) || !strings.Contains(result, `"status":200`) {
		t.Errorf("result = %q, want body forwarded", result)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 fetch call, got %d", len(calls))
	}
	if calls[0].Host != "example.com" || calls[0].Path != "/ping" {
		t.Errorf("unexpected call: %+v", calls[0])
	}
}

// TestFetchMock_CredentialsInjectedBeforeMock proves that credential injection
// runs BEFORE the mock RoundTripper sees the request — i.e. the prepareRequest
// pipeline executes end-to-end in production code paths.
func TestFetchMock_CredentialsInjectedBeforeMock(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("example", "default", "token"): []byte("test-token"),
	})
	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"example.com"},
			PathPrefix:     "/",
			ModuleName:     "example",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("example", "default"),
			Type:           transport.CredentialTypeBearer,
			Method:         transport.InjectionMethodBearerHeader,
		},
	}
	injector := transport.NewCredentialInjector(rules, store)

	mock := tooltest.NewFetchMock().
		JSON("example.com/ping", `{"ok":true}`)

	prepared := tooltest.PrepareToolset(t,
		tooltest.DistPackageDecl("fetch-test"),
		toolset.Config{
			FetchTransport: mock,
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("fixtures.local/fetch-test"): {
					Injector:  injector,
					Allowlist: transport.NewHostAllowlist([]string{"example.com"}),
				},
			},
		})

	if _, err := invoke.Run(prepared, "fetchTest.get", invokeArgs(t, map[string]any{
		"url": "https://example.com/ping",
	})); err != nil {
		t.Fatalf("invoke.Run: %v", err)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 fetch call, got %d", len(calls))
	}
	if got := calls[0].Headers.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
	}
}

// TestFetchMock_CrossHostRedirectStripsSensitiveHeaders verifies that the
// fetch transport strips Authorization (and similar sensitive headers) when
// following a cross-host redirect.
func TestFetchMock_CrossHostRedirectStripsSensitiveHeaders(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestSecretStore()
	store.Seed(map[string][]byte{
		credpath.Shared("a-example", "default", "token"): []byte("secret"),
	})
	rules := []transport.InjectionRule{
		{
			Hosts:          []string{"a.example.com"},
			PathPrefix:     "/",
			ModuleName:     "a-example",
			CredentialName: "default",
			SecretPrefix:   credpath.SharedPrefix("a-example", "default"),
			Type:           transport.CredentialTypeBearer,
			Method:         transport.InjectionMethodBearerHeader,
		},
	}
	injector := transport.NewCredentialInjector(rules, store)

	mock := tooltest.NewFetchMock().
		HandleFunc("a.example.com/start", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://b.example.com/end")
			w.WriteHeader(http.StatusFound)
		}).
		HandleFunc("b.example.com/end", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

	prepared := tooltest.PrepareToolset(t,
		tooltest.DistPackageDecl("fetch-test"),
		toolset.Config{
			FetchTransport: mock,
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("fixtures.local/fetch-test"): {
					Injector:  injector,
					Allowlist: transport.NewHostAllowlist([]string{"a.example.com", "b.example.com"}),
				},
			},
		})

	if _, err := invoke.Run(prepared, "fetchTest.get", invokeArgs(t, map[string]any{
		"url": "https://a.example.com/start",
	})); err != nil {
		t.Fatalf("invoke.Run: %v", err)
	}

	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 fetch calls (redirect), got %d", len(calls))
	}
	if got := calls[0].Headers.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("first hop Authorization = %q, want %q", got, "Bearer secret")
	}
	if got := calls[1].Headers.Get("Authorization"); got != "" {
		t.Errorf("second hop Authorization = %q, want empty (stripped on cross-host redirect)", got)
	}
}
