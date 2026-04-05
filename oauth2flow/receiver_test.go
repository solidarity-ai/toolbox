package oauth2flow_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/oauth2flow"
)

func TestCallbackReceiver_ReceivesCode(t *testing.T) {
	t.Parallel()

	recv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		t.Fatalf("NewCallbackReceiver: %v", err)
	}
	defer recv.Close()

	uri := recv.RedirectURI()
	if !strings.HasPrefix(uri, "http://127.0.0.1:") {
		t.Fatalf("RedirectURI = %q, want http://127.0.0.1:*/callback", uri)
	}

	// Simulate the OAuth provider redirecting.
	go func() {
		http.Get(uri + "?code=test-auth-code")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := recv.ReceiveCode(ctx, "")
	if err != nil {
		t.Fatalf("ReceiveCode: %v", err)
	}
	if code != "test-auth-code" {
		t.Fatalf("code = %q, want %q", code, "test-auth-code")
	}
}

func TestCallbackReceiver_ContextCancellation(t *testing.T) {
	t.Parallel()

	recv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		t.Fatalf("NewCallbackReceiver: %v", err)
	}
	defer recv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = recv.ReceiveCode(ctx, "")
	if err != context.Canceled {
		t.Fatalf("ReceiveCode error = %v, want context.Canceled", err)
	}
}

func TestManualReceiver_ReadsCode(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader("my-auth-code\n")
	recv := oauth2flow.NewManualReceiver(reader, "http://localhost:8080/callback")

	if recv.RedirectURI() != "http://localhost:8080/callback" {
		t.Fatalf("RedirectURI = %q, want override URI", recv.RedirectURI())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := recv.ReceiveCode(ctx, "")
	if err != nil {
		t.Fatalf("ReceiveCode: %v", err)
	}
	if code != "my-auth-code" {
		t.Fatalf("code = %q, want %q", code, "my-auth-code")
	}
}

func TestManualReceiver_DefaultOOBRedirectURI(t *testing.T) {
	t.Parallel()

	recv := oauth2flow.NewManualReceiver(strings.NewReader("code\n"), "")
	if recv.RedirectURI() != "urn:ietf:wg:oauth:2.0:oob" {
		t.Fatalf("RedirectURI = %q, want OOB URI", recv.RedirectURI())
	}
}

func TestManualReceiver_EmptyInput(t *testing.T) {
	t.Parallel()

	recv := oauth2flow.NewManualReceiver(strings.NewReader("\n"), "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := recv.ReceiveCode(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

type cancellableLineReader struct {
	lines chan string
}

func (r *cancellableLineReader) Read(_ []byte) (int, error) { return 0, io.EOF }

func (r *cancellableLineReader) ReadLine(ctx context.Context) (string, error) {
	select {
	case line := <-r.lines:
		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestManualReceiver_ContextAwareReaderCancellationDoesNotConsumeLaterInput(t *testing.T) {
	t.Parallel()

	reader := &cancellableLineReader{lines: make(chan string, 1)}
	recv := oauth2flow.NewManualReceiver(reader, "")

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := recv.ReceiveCode(cancelledCtx, "")
	if err != context.Canceled {
		t.Fatalf("ReceiveCode(cancelled) error = %v, want context.Canceled", err)
	}

	reader.lines <- "later-code"

	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()

	code, err := recv.ReceiveCode(ctx, "")
	if err != nil {
		t.Fatalf("ReceiveCode(second): %v", err)
	}
	if code != "later-code" {
		t.Fatalf("code = %q, want %q", code, "later-code")
	}
}

// mockReceiver is a test helper that returns a code after a delay.
type mockReceiver struct {
	uri   string
	code  string
	err   error
	delay time.Duration
}

func (m *mockReceiver) RedirectURI() string { return m.uri }
func (m *mockReceiver) ReceiveCode(ctx context.Context, _ string) (string, error) {
	select {
	case <-time.After(m.delay):
		return m.code, m.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (m *mockReceiver) Close() error { return nil }

func TestRaceReceiver_FirstWins(t *testing.T) {
	t.Parallel()

	fast := &mockReceiver{code: "fast-code", delay: 0}
	slow := &mockReceiver{code: "slow-code", delay: 5 * time.Second}

	race := oauth2flow.NewRaceReceiver("http://localhost/callback", fast, slow)
	defer race.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := race.ReceiveCode(ctx, "")
	if err != nil {
		t.Fatalf("ReceiveCode: %v", err)
	}
	if code != "fast-code" {
		t.Fatalf("code = %q, want %q", code, "fast-code")
	}
}

func TestRaceReceiver_AllFail(t *testing.T) {
	t.Parallel()

	fail1 := &mockReceiver{err: fmt.Errorf("fail1"), delay: 0}
	fail2 := &mockReceiver{err: fmt.Errorf("fail2"), delay: 0}

	race := oauth2flow.NewRaceReceiver("http://localhost/callback", fail1, fail2)
	defer race.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := race.ReceiveCode(ctx, "")
	if err == nil {
		t.Fatal("expected error when all receivers fail")
	}
	if !strings.Contains(err.Error(), "all receivers failed") {
		t.Fatalf("error = %v, want 'all receivers failed'", err)
	}
}
