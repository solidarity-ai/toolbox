package transport_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
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
