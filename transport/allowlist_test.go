package transport

import "testing"

func TestHostAllowlist_ExactMatch(t *testing.T) {
	t.Parallel()
	al := NewHostAllowlist([]string{"api.slack.com", "example.com"})

	tests := []struct {
		host string
		want bool
	}{
		{"api.slack.com", true},
		{"example.com", true},
		{"other.com", false},
	}
	for _, tt := range tests {
		if got := al.Allows(tt.host); got != tt.want {
			t.Errorf("Allows(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestHostAllowlist_WildcardMatch(t *testing.T) {
	t.Parallel()
	al := NewHostAllowlist([]string{"*.googleapis.com"})

	tests := []struct {
		host string
		want bool
	}{
		{"sheets.googleapis.com", true},
		{"drive.googleapis.com", true},
		{"googleapis.com", false},
		{"evil.com", false},
	}
	for _, tt := range tests {
		if got := al.Allows(tt.host); got != tt.want {
			t.Errorf("Allows(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestHostAllowlist_GlobalWildcardMatch(t *testing.T) {
	t.Parallel()
	al := NewHostAllowlist([]string{"*"})

	tests := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"localhost", true},
		{"127.0.0.1", true},
	}
	for _, tt := range tests {
		if got := al.Allows(tt.host); got != tt.want {
			t.Errorf("Allows(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestHostAllowlist_EmptyDeniesAll(t *testing.T) {
	t.Parallel()
	al := NewHostAllowlist(nil)

	if al.Allows("anything.com") {
		t.Error("empty allowlist should deny all hosts")
	}
}

func TestHostAllowlist_NoMatchDenied(t *testing.T) {
	t.Parallel()
	al := NewHostAllowlist([]string{"api.slack.com", "*.googleapis.com"})

	tests := []struct {
		host string
	}{
		{"evil.com"},
		{"slack.com"},
		{"notgoogleapis.com"},
	}
	for _, tt := range tests {
		if al.Allows(tt.host) {
			t.Errorf("Allows(%q) = true, want false", tt.host)
		}
	}
}
