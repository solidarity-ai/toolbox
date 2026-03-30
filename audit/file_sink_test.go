package audit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/audit"
)

func TestFileSinkWritesJSONLRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink(): %v", err)
	}

	event, err := audit.NewCredentialRefreshSuccess("workspace", "github.com/example/google-workspace:workspace", time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("NewCredentialRefreshSuccess(): %v", err)
	}
	if !sink.TryEmit(event) {
		t.Fatal("TryEmit() = false, want true")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1 (raw=%q)", len(lines), string(raw))
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("Unmarshal(record): %v", err)
	}
	if got := record["name"]; got != "credential_refresh" {
		t.Fatalf("name = %#v, want credential_refresh", got)
	}
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v, want object", record["payload"])
	}
	if got := payload["credential"]; got != "workspace" {
		t.Fatalf("payload.credential = %#v, want workspace", got)
	}
	if got := payload["outcome"]; got != "success" {
		t.Fatalf("payload.outcome = %#v, want success", got)
	}
}

func TestFileSinkCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "audit", "events.jsonl")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink(): %v", err)
	}
	event, err := audit.NewCredentialDenied("gmail.googleapis.com", "not allowed by policy", "workspace", "bearer_header")
	if err != nil {
		t.Fatalf("NewCredentialDenied(): %v", err)
	}
	if !sink.TryEmit(event) {
		t.Fatal("TryEmit() = false, want true")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
}

func TestFileSinkRejectsEmptyPath(t *testing.T) {
	if _, err := audit.NewFileSink(""); err == nil {
		t.Fatal("NewFileSink(\"\") error = nil, want error")
	}
}
