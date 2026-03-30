package invoke

import (
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestGoFetchWithTransportPolicySurfacesPolicyErrorsBeforeOutboundFetch(t *testing.T) {
	t.Parallel()

	secretStore := testutil.NewTestSecretStore()
	secretStore.SeedStrings(map[string]string{
		"github.com/example/github-issues/github_token": "test-token",
	})

	policy, err := transport.NewPolicy(secretStore, []transport.Rule{{
		Name:      "github_token",
		SecretKey: "github.com/example/github-issues/github_token",
		Inject: tooldef.CredentialInject{
			Hosts:  []string{"api.github.com"},
			Method: "bearer_header",
		},
	}}, nil)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}

	fetchFn := goFetchWithTransportPolicy(policy)

	t.Run("invalid url still evaluates transport policy boundary", func(t *testing.T) {
		_, err := fetchFn("://bad-url", "GET", "not-json", "")
		if err == nil {
			t.Fatal("expected invalid url to fail")
		}
		if !strings.Contains(err.Error(), "parse request url") {
			t.Fatalf("error = %v, want parse request url context", err)
		}
	})
}
