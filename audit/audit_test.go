package audit_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/audit"
)

func TestEventConstructorsEnforceRedactedContract(t *testing.T) {
	t.Parallel()

	t.Run("credential injected requires host credential and method", func(t *testing.T) {
		t.Parallel()
		if _, err := audit.NewCredentialInjected("", "github_token", "bearer_header"); err == nil {
			t.Fatal("expected missing host to fail validation")
		}
		if _, err := audit.NewCredentialInjected("api.github.com", "", "bearer_header"); err == nil {
			t.Fatal("expected missing credential to fail validation")
		}
		if _, err := audit.NewCredentialInjected("api.github.com", "github_token", ""); err == nil {
			t.Fatal("expected missing method to fail validation")
		}
	})

	t.Run("credential denied requires host and reason only", func(t *testing.T) {
		t.Parallel()
		if _, err := audit.NewCredentialDenied("", "not_allowed_by_policy", "", ""); err == nil {
			t.Fatal("expected missing host to fail validation")
		}
		if _, err := audit.NewCredentialDenied("api.github.com", "", "", ""); err == nil {
			t.Fatal("expected missing reason to fail validation")
		}
	})
}

func TestCollectorStoresDeterministicSecretSafeEvents(t *testing.T) {
	t.Parallel()

	collector := audit.NewCollector()
	injected, err := audit.NewCredentialInjected("api.github.com", "github_token", "bearer_header")
	if err != nil {
		t.Fatalf("NewCredentialInjected: %v", err)
	}
	denied, err := audit.NewCredentialDenied("example.com", "not_allowed_by_policy", "github_token", "bearer_header")
	if err != nil {
		t.Fatalf("NewCredentialDenied: %v", err)
	}

	if ok := audit.Emit(collector, injected); !ok {
		t.Fatal("Emit injected event = false, want true")
	}
	if ok := audit.Emit(collector, denied); !ok {
		t.Fatal("Emit denied event = false, want true")
	}

	want := []audit.Event{injected, denied}
	if diff := cmp.Diff(want, collector.Events()); diff != "" {
		t.Fatalf("collector events mismatch (-want +got):\n%s", diff)
	}
}

func TestEmitFallsBackToNoopAndRejectsInvalidEvents(t *testing.T) {
	t.Parallel()

	injected, err := audit.NewCredentialInjected("api.github.com", "github_token", "bearer_header")
	if err != nil {
		t.Fatalf("NewCredentialInjected: %v", err)
	}
	if ok := audit.Emit(nil, injected); ok {
		t.Fatal("Emit(nil, validEvent) = true, want false for noop sink")
	}

	if ok := audit.Emit(audit.NewCollector(), audit.Event{}); ok {
		t.Fatal("Emit(invalidEvent) = true, want false")
	}
}
