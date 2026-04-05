package toolset

import "testing"

func TestExtractAccountParams_PrecisionScoping(t *testing.T) {
	t.Parallel()

	fullParams := map[string]any{
		"google_workspace_account": "admin@acme.com",
		"billing_account":          "acct-123",
		"query":                    "test",
	}

	credNames := []string{"google_workspace"}
	got := extractAccountParams(fullParams, credNames)

	if len(got) != 1 {
		t.Fatalf("expected 1 account, got %d: %v", len(got), got)
	}
	if got["google_workspace"] != "admin@acme.com" {
		t.Fatalf("google_workspace = %q, want admin@acme.com", got["google_workspace"])
	}
	if _, ok := got["billing"]; ok {
		t.Fatal("billing should not be extracted")
	}
}

func TestExtractAccountParams_MultiCredential(t *testing.T) {
	t.Parallel()

	fullParams := map[string]any{
		"google_workspace_account": "admin@acme.com",
		"google_calendar_account":  "personal@gmail.com",
		"query":                    "test",
	}

	credNames := []string{"google_workspace", "google_calendar", "slack"}
	got := extractAccountParams(fullParams, credNames)

	if len(got) != 2 {
		t.Fatalf("expected 2 accounts, got %d: %v", len(got), got)
	}
	if got["google_workspace"] != "admin@acme.com" {
		t.Fatalf("google_workspace = %q, want admin@acme.com", got["google_workspace"])
	}
	if got["google_calendar"] != "personal@gmail.com" {
		t.Fatalf("google_calendar = %q, want personal@gmail.com", got["google_calendar"])
	}
}
