package transport_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/audit"
	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestPolicy(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when no runtime policy is required", func(t *testing.T) {
		t.Parallel()

		policy, err := transport.NewPolicy(nil, nil, nil, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}
		if policy != nil {
			t.Fatalf("NewPolicy() = %#v, want nil", policy)
		}
	})

	t.Run("preserves deny by default runtime seam", func(t *testing.T) {
		t.Parallel()

		policy, err := transport.NewPolicy(nil, nil, nil, true)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}
		if policy == nil {
			t.Fatal("expected runtime policy")
		}
		if got := policy.AllowedHosts(); got != nil {
			t.Fatalf("AllowedHosts() = %v, want nil", got)
		}
		if got := policy.Rules(); got != nil {
			t.Fatalf("Rules() = %v, want nil", got)
		}
	})

	t.Run("normalizes allowlist metadata deterministically", func(t *testing.T) {
		t.Parallel()

		policy, err := transport.NewPolicy(nil, nil, []string{" *.googleapis.com ", "oauth2.googleapis.com", "oauth2.googleapis.com", ""}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}
		if policy == nil {
			t.Fatal("expected runtime policy")
		}
		want := []string{"*.googleapis.com", "oauth2.googleapis.com"}
		if diff := cmp.Diff(want, policy.AllowedHosts()); diff != "" {
			t.Fatalf("AllowedHosts() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("normalized empty allowlist still keeps forced runtime policy", func(t *testing.T) {
		t.Parallel()

		policy, err := transport.NewPolicy(nil, nil, []string{" ", "\n", ""}, true)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}
		if policy == nil {
			t.Fatal("expected runtime policy")
		}
		if got := policy.AllowedHosts(); got != nil {
			t.Fatalf("AllowedHosts() = %v, want nil", got)
		}
	})
}

func TestPolicyPrepareRequest(t *testing.T) {
	t.Parallel()

	t.Run("allows exact hosts while ignoring ports", func(t *testing.T) {
		t.Parallel()

		policy := mustNewPolicy(t, nil, nil, []string{"api.example.com"}, false)
		gotURL, err := policy.PrepareRequest(context.Background(), "https://api.example.com:8443/v1/issues", fetch.NewHeaders())
		if err != nil {
			t.Fatalf("PrepareRequest: %v", err)
		}
		if gotURL != "https://api.example.com:8443/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want unchanged", gotURL)
		}
	})

	t.Run("allows wildcard subdomains", func(t *testing.T) {
		t.Parallel()

		policy := mustNewPolicy(t, nil, nil, []string{"*.example.com"}, false)
		gotURL, err := policy.PrepareRequest(context.Background(), "https://api.example.com/v1/issues", fetch.NewHeaders())
		if err != nil {
			t.Fatalf("PrepareRequest: %v", err)
		}
		if gotURL != "https://api.example.com/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want unchanged", gotURL)
		}
	})

	t.Run("wildcard does not match bare parent host", func(t *testing.T) {
		t.Parallel()

		policy := mustNewPolicy(t, nil, nil, []string{"*.example.com"}, false)
		gotURL, err := policy.PrepareRequest(context.Background(), "https://example.com/v1/issues", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected bare parent host to be denied")
		}
		if gotURL != "https://example.com/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want original raw URL on denial", gotURL)
		}
		if !strings.Contains(err.Error(), `transport denied request to host "example.com": not allowed by policy`) {
			t.Fatalf("error = %v, want explicit denied-host context", err)
		}
	})

	t.Run("deny by default rejects when no allowlist is declared", func(t *testing.T) {
		t.Parallel()

		policy := mustNewPolicy(t, nil, nil, nil, true)
		gotURL, err := policy.PrepareRequest(context.Background(), "https://api.example.com/v1/issues", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected deny-by-default policy to reject request")
		}
		if gotURL != "https://api.example.com/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want original raw URL on denial", gotURL)
		}
		if !strings.Contains(err.Error(), `transport denied request to host "api.example.com": no allowed hosts declared`) {
			t.Fatalf("error = %v, want explicit deny-by-default context", err)
		}
	})

	t.Run("rejects malformed request urls before outbound fetch", func(t *testing.T) {
		t.Parallel()

		policy := mustNewPolicy(t, nil, nil, []string{"api.example.com"}, false)
		gotURL, err := policy.PrepareRequest(context.Background(), "://bad-url", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected malformed request url to fail")
		}
		if gotURL != "://bad-url" {
			t.Fatalf("PrepareRequest() url = %q, want original raw URL on parse failure", gotURL)
		}
		if !strings.Contains(err.Error(), "parse request url") {
			t.Fatalf("error = %v, want parse request url context", err)
		}
	})

	t.Run("injector mutations stay transport-owned when allowlist denies", func(t *testing.T) {
		t.Parallel()

		secretStore := testutil.NewTestSecretStore()
		secretStore.SeedStrings(map[string]string{"pkg/api_key": "secret-token"})

		policy := mustNewPolicy(t, secretStore, []transport.Rule{{
			Name:      "api_key",
			SecretKey: "pkg/api_key",
			Inject: tooldef.CredentialInject{
				Hosts:     []string{"api.example.com"},
				Method:    "api_key_query",
				QueryName: "token",
			},
		}}, []string{"example.invalid"}, false)

		gotURL, err := policy.PrepareRequest(context.Background(), "https://api.example.com/v1/issues", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected denied host after injector mutation")
		}
		if gotURL != "https://api.example.com/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want original raw URL when preflight denies", gotURL)
		}
		if strings.Contains(gotURL, "secret-token") {
			t.Fatalf("PrepareRequest() leaked injected secret in returned URL: %q", gotURL)
		}
		if !strings.Contains(err.Error(), `transport denied request to host "api.example.com": not allowed by policy`) {
			t.Fatalf("error = %v, want explicit denied-host context", err)
		}
	})
	t.Run("emits denied event for deny by default", func(t *testing.T) {
		t.Parallel()

		collector := audit.NewCollector()
		policy := mustNewPolicyWithAudit(t, nil, nil, nil, true, collector)

		_, err := policy.PrepareRequest(context.Background(), "https://api.example.com/v1/issues", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected deny-by-default policy to reject request")
		}

		want := []audit.Event{{
			Name: audit.EventCredentialDenied,
			Payload: audit.CredentialDenied{
				Host:   "api.example.com",
				Reason: "no_allowed_hosts_declared",
			},
		}}
		if diff := cmp.Diff(want, collector.Events()); diff != "" {
			t.Fatalf("denied audit mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("emits denied event with matched credential metadata when allowlist blocks injected request", func(t *testing.T) {
		t.Parallel()

		secretStore := testutil.NewTestSecretStore()
		secretStore.SeedStrings(map[string]string{"pkg/api_key": "secret-token"})
		collector := audit.NewCollector()

		policy := mustNewPolicyWithAudit(t, secretStore, []transport.Rule{{
			Name:      "api_key",
			SecretKey: "pkg/api_key",
			Inject: tooldef.CredentialInject{
				Hosts:     []string{"api.example.com"},
				Method:    "api_key_query",
				QueryName: "token",
			},
		}}, []string{"example.invalid"}, false, collector)

		gotURL, err := policy.PrepareRequest(context.Background(), "https://api.example.com/v1/issues", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected denied host after injector mutation")
		}
		if gotURL != "https://api.example.com/v1/issues" {
			t.Fatalf("PrepareRequest() url = %q, want original raw URL when preflight denies", gotURL)
		}

		want := []audit.Event{
			{
				Name: audit.EventCredentialInjected,
				Payload: audit.CredentialInjected{
					Host:         "api.example.com",
					Credential:   "api_key",
					InjectMethod: "api_key_query",
				},
			},
			{
				Name: audit.EventCredentialDenied,
				Payload: audit.CredentialDenied{
					Host:         "api.example.com",
					Reason:       "not_allowed_by_policy",
					Credential:   "api_key",
					InjectMethod: "api_key_query",
				},
			},
		}
		if diff := cmp.Diff(want, collector.Events()); diff != "" {
			t.Fatalf("audit mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("malformed urls emit no misleading audit events", func(t *testing.T) {
		t.Parallel()

		collector := audit.NewCollector()
		policy := mustNewPolicyWithAudit(t, nil, nil, []string{"api.example.com"}, false, collector)

		_, err := policy.PrepareRequest(context.Background(), "://bad-url", fetch.NewHeaders())
		if err == nil {
			t.Fatal("expected malformed request url to fail")
		}
		if got := collector.Events(); len(got) != 0 {
			t.Fatalf("collector events = %v, want no audit events on malformed url", got)
		}
	})
}

func mustNewPolicy(t *testing.T, store secrets.SecretStore, rules []transport.Rule, allowedHosts []string, requireRuntimePolicy bool) *transport.Policy {
	t.Helper()

	policy, err := transport.NewPolicy(store, rules, allowedHosts, requireRuntimePolicy)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	if policy == nil {
		t.Fatal("expected runtime policy")
	}
	return policy
}

func mustNewPolicyWithAudit(t *testing.T, store secrets.SecretStore, rules []transport.Rule, allowedHosts []string, requireRuntimePolicy bool, sink audit.Sink) *transport.Policy {
	t.Helper()

	policy, err := transport.NewPolicyWithOptions(store, rules, allowedHosts, requireRuntimePolicy, transport.WithAuditSink(sink))
	if err != nil {
		t.Fatalf("NewPolicyWithOptions: %v", err)
	}
	if policy == nil {
		t.Fatal("expected runtime policy")
	}
	return policy
}
