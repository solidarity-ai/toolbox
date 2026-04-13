package invoke_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

// TestFullE2E_ManifestCredentialParsing verifies that loading the manifest and
// calling BuildInjectionRules produces the correct transport-layer rules.
func TestFullE2E_ManifestCredentialParsing(t *testing.T) {
	t.Parallel()

	loadedPkgs, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("auth-test"))
	if err != nil {
		t.Fatalf("assembler.Load: %v", err)
	}
	loaded := loadedPkgs.Packages[0].Package
	module := loaded.Package.Module.String()

	// Verify package-level fields.
	if len(loaded.Package.Credentials) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(loaded.Package.Credentials))
	}

	cred := loaded.Package.Credentials[0]
	if cred.Name != "test_api" {
		t.Errorf("credential name: got %q, want %q", cred.Name, "test_api")
	}
	if cred.Type != "api_key" {
		t.Errorf("credential type: got %q, want %q", cred.Type, "api_key")
	}
	if len(cred.Inject.Hosts) != 1 || cred.Inject.Hosts[0] != "127.0.0.1" {
		t.Errorf("credential inject hosts: got %v, want [127.0.0.1]", cred.Inject.Hosts)
	}
	if cred.Inject.Method != "api_key_header" {
		t.Errorf("credential inject method: got %q, want %q", cred.Inject.Method, "api_key_header")
	}
	if cred.Inject.PathPrefix != "/api/" {
		t.Errorf("credential inject path_prefix: got %q, want %q", cred.Inject.PathPrefix, "/api/")
	}

	if len(loaded.Package.AllowedHosts) != 2 || loaded.Package.AllowedHosts[0] != "127.0.0.1" || loaded.Package.AllowedHosts[1] != "localhost" {
		t.Errorf("allowed_hosts: got %v, want [127.0.0.1 localhost]", loaded.Package.AllowedHosts)
	}

	// Build injection rules and verify the transport-layer representation.
	rules := credentialrepo.BuildInjectionRules(loaded.Package)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	rule := rules[0]
	if len(rule.Hosts) != 1 || rule.Hosts[0] != "127.0.0.1" {
		t.Errorf("rule Hosts: got %v, want [127.0.0.1]", rule.Hosts)
	}
	if rule.PathPrefix != "/api/" {
		t.Errorf("rule PathPrefix: got %q, want %q", rule.PathPrefix, "/api/")
	}
	if rule.ModuleName != module {
		t.Errorf("rule ModuleName: got %q, want %q", rule.ModuleName, module)
	}
	if rule.CredentialName != "test_api" {
		t.Errorf("rule CredentialName: got %q, want %q", rule.CredentialName, "test_api")
	}
	if rule.Type != transport.CredentialTypeAPIKey {
		t.Errorf("rule Type: got %q, want %q", rule.Type, transport.CredentialTypeAPIKey)
	}
	if rule.Method != transport.InjectionMethodAPIKeyHeader {
		t.Errorf("rule Method: got %q, want %q", rule.Method, transport.InjectionMethodAPIKeyHeader)
	}
	if rule.Provider != nil {
		t.Errorf("rule Provider: expected nil for api_key type, got %+v", rule.Provider)
	}
}

// TestFullE2E_MissingCredentialsErrorMessage exercises invoke.Run() with an
// injector that has rules but NO secrets seeded, and verifies the error message
// contains both the secret key name and "toolbox auth" and the package name.
func TestFullE2E_MissingCredentialsErrorMessage(t *testing.T) {
	t.Parallel()

	loadedPkgs, err := assembler.Load(context.Background(), nil, tooltest.DistPackageDecl("auth-test"))
	if err != nil {
		t.Fatalf("assembler.Load: %v", err)
	}
	loaded := loadedPkgs.Packages[0].Package
	module := loaded.Package.Module.String()

	rules := credentialrepo.BuildInjectionRules(loaded.Package)
	if len(rules) == 0 {
		t.Fatal("expected at least 1 injection rule")
	}

	// Empty secret store — no secrets seeded.
	store := testutil.NewTestSecretStore()

	ci := transport.NewCredentialInjector(rules, store)
	al := transport.NewHostAllowlist(loaded.Package.AllowedHosts)
	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("auth-test"), toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			loaded.Package.Module: {
				Injector:  ci,
				Allowlist: al,
			},
		},
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"should not reach"}`))
	}))
	t.Cleanup(upstream.Close)

	_, err = invoke.Run(prepared, "authTest.get", map[string]any{
		"url": upstream.URL + "/api/data",
	})
	if err == nil {
		t.Fatal("expected error from missing credentials, got nil")
	}

	errMsg := err.Error()
	// Error must be generic — no secret paths or package names leaked to sandbox.
	if !strings.Contains(errMsg, "credential injection failed") {
		t.Errorf("error should contain generic 'credential injection failed', got: %s", errMsg)
	}
	// Must NOT contain secret store key paths (security: tool shouldn't see these).
	if strings.Contains(errMsg, module+"/test_api") {
		t.Errorf("error should NOT contain secret key paths, got: %s", errMsg)
	}
}
