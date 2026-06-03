package oauth2flow_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/oauth2flow"
	"golang.org/x/oauth2"
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

func TestCallbackReceiver_CloseWaitsForInFlightCallback(t *testing.T) {
	t.Parallel()

	recv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		t.Fatalf("NewCallbackReceiver: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		resp, err := http.Get(recv.RedirectURI() + "?code=close-waits-code")
		if err != nil {
			done <- err
			return
		}
		resp.Body.Close()
		done <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, err := recv.ReceiveCode(ctx, "")
	if err != nil {
		t.Fatalf("ReceiveCode: %v", err)
	}
	if code != "close-waits-code" {
		t.Fatalf("code = %q, want close-waits-code", code)
	}
	if err := recv.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("in-flight callback request failed: %v", err)
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

func TestCallbackReceiver_ReportsProviderError(t *testing.T) {
	t.Parallel()

	recv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		t.Fatalf("NewCallbackReceiver: %v", err)
	}
	defer recv.Close()

	go func() {
		http.Get(recv.RedirectURI() + "?error=access_denied&error_description=user+denied&state=test-state")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = recv.ReceiveCode(ctx, "test-state")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !strings.Contains(err.Error(), "access_denied") || !strings.Contains(err.Error(), "user denied") {
		t.Fatalf("ReceiveCode error = %v, want provider error details", err)
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

func TestManualReceiver_ExtractsCodeFromRedirectURL(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader("http://127.0.0.1:8080/callback?code=my-auth-code&state=test-state\n")
	recv := oauth2flow.NewManualReceiver(reader, "http://127.0.0.1:8080/callback")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := recv.ReceiveCode(ctx, "test-state")
	if err != nil {
		t.Fatalf("ReceiveCode: %v", err)
	}
	if code != "my-auth-code" {
		t.Fatalf("code = %q, want %q", code, "my-auth-code")
	}
}

func TestManualReceiver_RejectsRedirectURLWithMismatchedState(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader("http://127.0.0.1:8080/callback?code=my-auth-code&state=wrong-state\n")
	recv := oauth2flow.NewManualReceiver(reader, "http://127.0.0.1:8080/callback")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := recv.ReceiveCode(ctx, "expected-state")
	if err == nil {
		t.Fatal("expected state mismatch error")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("ReceiveCode error = %v, want state mismatch", err)
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

type mockStartingReceiver struct {
	mockReceiver
	starts []mockReceiverStart
	err    error
}

type mockReceiverStart struct {
	state string
	url   string
}

func (m *mockStartingReceiver) StartAuthorization(_ context.Context, state string, authorizationURL string) error {
	m.starts = append(m.starts, mockReceiverStart{state: state, url: authorizationURL})
	return m.err
}

func TestAuthorizeCodeStartsAuthorizationBeforeCallbackAndReceive(t *testing.T) {
	t.Parallel()

	receiver := &mockStartingReceiver{mockReceiver: mockReceiver{
		uri:  "http://localhost/callback",
		code: "test-code",
	}}
	cfg := &oauth2.Config{
		ClientID:    "client-id",
		RedirectURL: "will-be-replaced",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://provider.example.test/authorize",
			TokenURL: "https://provider.example.test/token",
		},
		Scopes: []string{"scope-a"},
	}

	var callbackURL string
	code, err := oauth2flow.AuthorizeCode(context.Background(), cfg, receiver, "", nil, func(authorizationURL string) {
		if len(receiver.starts) != 1 {
			t.Fatalf("onAuthURL ran before StartAuthorization: starts=%#v", receiver.starts)
		}
		callbackURL = authorizationURL
	})
	if err != nil {
		t.Fatalf("AuthorizeCode(): %v", err)
	}
	if code != "test-code" {
		t.Fatalf("code = %q, want test-code", code)
	}
	if len(receiver.starts) != 1 {
		t.Fatalf("starts = %#v, want one StartAuthorization call", receiver.starts)
	}
	start := receiver.starts[0]
	if start.state == "" {
		t.Fatal("StartAuthorization state is empty")
	}
	if start.url == "" || start.url != callbackURL {
		t.Fatalf("StartAuthorization URL = %q, onAuthURL URL = %q", start.url, callbackURL)
	}
	parsed, err := url.Parse(start.url)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if got := parsed.Query().Get("state"); got != start.state {
		t.Fatalf("authorization URL state = %q, StartAuthorization state = %q", got, start.state)
	}
	if got := parsed.Query().Get("redirect_uri"); got != "http://localhost/callback" {
		t.Fatalf("authorization URL redirect_uri = %q, want receiver redirect URI", got)
	}
}

func TestRaceReceiverStartAuthorizationForwardsToStartingChildren(t *testing.T) {
	t.Parallel()

	first := &mockStartingReceiver{}
	manual := &mockReceiver{}
	second := &mockStartingReceiver{}
	race := oauth2flow.NewRaceReceiver("http://localhost/callback", first, manual, second)

	if err := race.StartAuthorization(context.Background(), "state-123", "https://provider.example.test/auth?state=state-123"); err != nil {
		t.Fatalf("StartAuthorization(): %v", err)
	}
	for name, receiver := range map[string]*mockStartingReceiver{"first": first, "second": second} {
		if len(receiver.starts) != 1 {
			t.Fatalf("%s starts = %#v, want one", name, receiver.starts)
		}
		if receiver.starts[0].state != "state-123" || receiver.starts[0].url != "https://provider.example.test/auth?state=state-123" {
			t.Fatalf("%s start = %#v, want forwarded state/url", name, receiver.starts[0])
		}
	}
}

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

func TestRaceReceiver_ProviderErrorWinsImmediately(t *testing.T) {
	t.Parallel()

	callbackRecv, err := oauth2flow.NewCallbackReceiver()
	if err != nil {
		t.Fatalf("NewCallbackReceiver: %v", err)
	}
	defer callbackRecv.Close()

	slow := &mockReceiver{code: "slow-code", delay: 5 * time.Second}
	race := oauth2flow.NewRaceReceiver(callbackRecv.RedirectURI(), callbackRecv, slow)
	defer race.Close()

	go func() {
		http.Get(callbackRecv.RedirectURI() + "?error=access_denied&error_description=user+denied&state=test-state")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err = race.ReceiveCode(ctx, "test-state")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("ReceiveCode error = %v, want provider error", err)
	}
	if strings.Contains(err.Error(), "all receivers failed") {
		t.Fatalf("ReceiveCode error = %v, want direct provider error", err)
	}
}
