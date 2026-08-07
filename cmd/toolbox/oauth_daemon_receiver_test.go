package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/oauth2flow"
	"golang.org/x/oauth2"
)

type stubDaemonOAuthClient struct {
	mu          sync.Mutex
	redirects   daemon.OAuthRedirectURLs
	redirectErr error
	beginErr    error
	beginFlowID string
	emptyFlowID bool
	waitResult  daemon.OAuthWaitResult
	waitErr     error
	begins      []daemon.OAuthBeginOptions
	waits       []string
	cancels     []string
}

func (s *stubDaemonOAuthClient) OAuthRedirectURI(context.Context) (daemon.OAuthRedirectURLs, error) {
	if s.redirectErr != nil {
		return daemon.OAuthRedirectURLs{}, s.redirectErr
	}
	return s.redirects, nil
}

func (s *stubDaemonOAuthClient) OAuthBegin(_ context.Context, opts daemon.OAuthBeginOptions) (daemon.OAuthFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.begins = append(s.begins, opts)
	if s.beginErr != nil {
		return daemon.OAuthFlow{}, s.beginErr
	}
	flowID := s.beginFlowID
	if flowID == "" && !s.emptyFlowID {
		flowID = "flow-123"
	}
	return daemon.OAuthFlow{FlowID: flowID, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func (s *stubDaemonOAuthClient) OAuthWait(_ context.Context, flowID string) (daemon.OAuthWaitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, flowID)
	if s.waitErr != nil {
		return daemon.OAuthWaitResult{}, s.waitErr
	}
	return s.waitResult, nil
}

func (s *stubDaemonOAuthClient) OAuthCancel(_ context.Context, flowID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels = append(s.cancels, flowID)
	return nil
}

func TestDaemonOAuthReceiverRegistersWaitsAndCancels(t *testing.T) {
	client := &stubDaemonOAuthClient{
		redirects: daemon.OAuthRedirectURLs{
			RedirectURI: "http://localhost:7777/oauth2/callback",
			DaemonURL:   "http://localhost:7777/",
		},
		waitResult: daemon.OAuthWaitResult{Code: "code-123", State: "state-123"},
	}
	receiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize Calendar for workspace")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	if receiver.RedirectURI() != "http://localhost:7777/oauth2/callback" {
		t.Fatalf("RedirectURI() = %q", receiver.RedirectURI())
	}
	if receiver.DaemonURL() != "http://localhost:7777/" {
		t.Fatalf("DaemonURL() = %q", receiver.DaemonURL())
	}

	if err := receiver.StartAuthorization(context.Background(), "state-123", "https://provider.example.test/auth?state=state-123"); err != nil {
		t.Fatalf("StartAuthorization(): %v", err)
	}
	code, err := receiver.ReceiveCode(context.Background(), "state-123")
	if err != nil {
		t.Fatalf("ReceiveCode(): %v", err)
	}
	if code != "code-123" {
		t.Fatalf("code = %q, want code-123", code)
	}
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.begins) != 1 {
		t.Fatalf("begins = %#v, want one", client.begins)
	}
	if got := client.begins[0]; got.State != "state-123" ||
		got.AuthorizationURL != "https://provider.example.test/auth?state=state-123" ||
		got.Label != "Authorize Calendar for workspace" {
		t.Fatalf("begin options = %#v, want state/url/label", got)
	}
	if len(client.waits) != 1 || client.waits[0] != "flow-123" {
		t.Fatalf("waits = %#v, want flow-123", client.waits)
	}
	if len(client.cancels) != 1 || client.cancels[0] != "flow-123" {
		t.Fatalf("cancels = %#v, want flow-123", client.cancels)
	}
}

func TestDaemonOAuthReceiverProviderErrorUsesOAuth2FlowError(t *testing.T) {
	client := &stubDaemonOAuthClient{
		redirects: daemon.OAuthRedirectURLs{
			RedirectURI: "http://localhost:7777/oauth2/callback",
			DaemonURL:   "http://localhost:7777/",
		},
		waitResult: daemon.OAuthWaitResult{
			State:            "state-provider-error",
			Error:            "access_denied",
			ErrorDescription: "user denied",
			ErrorURI:         "https://provider.example.test/help",
		},
	}
	receiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	if err := receiver.StartAuthorization(context.Background(), "state-provider-error", "https://provider.example.test/auth"); err != nil {
		t.Fatalf("StartAuthorization(): %v", err)
	}
	_, err = receiver.ReceiveCode(context.Background(), "state-provider-error")
	if err == nil {
		t.Fatal("ReceiveCode() error = nil, want provider error")
	}
	if !strings.Contains(err.Error(), "provider returned error access_denied") ||
		!strings.Contains(err.Error(), "user denied") ||
		!strings.Contains(err.Error(), "https://provider.example.test/help") {
		t.Fatalf("ReceiveCode() error = %v, want OAuth provider error details", err)
	}
}

func TestDaemonOAuthReceiverRejectsInvalidAdvertisedURLs(t *testing.T) {
	tests := []struct {
		name      string
		redirects daemon.OAuthRedirectURLs
	}{
		{
			name:      "empty redirect",
			redirects: daemon.OAuthRedirectURLs{DaemonURL: "http://localhost:7777/"},
		},
		{
			name:      "non-http redirect",
			redirects: daemon.OAuthRedirectURLs{RedirectURI: "urn:ietf:wg:oauth:2.0:oob", DaemonURL: "http://localhost:7777/"},
		},
		{
			name:      "empty daemon URL",
			redirects: daemon.OAuthRedirectURLs{RedirectURI: "http://localhost:7777/oauth2/callback"},
		},
		{
			name:      "non-http daemon URL",
			redirects: daemon.OAuthRedirectURLs{RedirectURI: "http://localhost:7777/oauth2/callback", DaemonURL: "file:///tmp/toolbox"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newDaemonOAuthReceiver(context.Background(), &stubDaemonOAuthClient{redirects: tt.redirects}, "Authorize")
			if err == nil {
				t.Fatal("newDaemonOAuthReceiver() error = nil, want invalid URL error")
			}
		})
	}
}

func TestDaemonOAuthReceiverPropagatesBeginErrorBeforeFlowID(t *testing.T) {
	client := &stubDaemonOAuthClient{
		redirects: daemon.OAuthRedirectURLs{
			RedirectURI: "http://localhost:7777/oauth2/callback",
			DaemonURL:   "http://localhost:7777/",
		},
		beginErr: errors.New("begin failed"),
	}
	receiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	if err := receiver.StartAuthorization(context.Background(), "state", "https://provider.example.test/auth"); err == nil {
		t.Fatal("StartAuthorization() error = nil, want begin error")
	}
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if len(client.cancels) != 0 {
		t.Fatalf("cancels = %#v, want none without flow_id", client.cancels)
	}
}

func TestDaemonOAuthReceiverAuthorizeCodeWithRealDaemon(t *testing.T) {
	useShortDaemonDir(t)
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	udsServer, closeUDS := startTestDaemonServer(t)
	defer closeUDS()

	closeDebug, addr, err := startDaemonDebugServer(io.Discard, nil, udsServer)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeDebug(); err != nil {
			t.Fatalf("close debug server: %v", err)
		}
	}()

	client, err := daemon.EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client Close(): %v", err)
		}
	}()

	receiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize Calendar for workspace")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	defer func() {
		if err := receiver.Close(); err != nil {
			t.Fatalf("receiver Close(): %v", err)
		}
	}()

	cfg := &oauth2.Config{
		ClientID: "client-id",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://provider.example.test/authorize",
			TokenURL: "https://provider.example.test/token",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	code, err := oauth2flow.AuthorizeCode(ctx, cfg, receiver, "", nil, func(authorizationURL string) {
		parsed, err := url.Parse(authorizationURL)
		if err != nil {
			t.Errorf("parse authorization URL: %v", err)
			return
		}
		state := parsed.Query().Get("state")
		if state == "" {
			t.Errorf("authorization URL missing state: %q", authorizationURL)
			return
		}
		resp, body := getDaemonHTTP(t, "http://"+addr+"/oauth2/callback?code=daemon-code&state="+url.QueryEscape(state))
		if resp.StatusCode != 200 {
			t.Errorf("callback status = %d; body=%q", resp.StatusCode, body)
		}
	})
	if err != nil {
		t.Fatalf("AuthorizeCode(): %v", err)
	}
	if code != "daemon-code" {
		t.Fatalf("code = %q, want daemon-code", code)
	}
	if got := udsServer.PendingOAuthFlows(); len(got) != 0 {
		t.Fatalf("PendingOAuthFlows() = %#v, want flow consumed after wait", got)
	}
}

func TestDaemonOAuthReceiverManualWinsCancelsDaemonFlow(t *testing.T) {
	client := &stubDaemonOAuthClient{
		redirects: daemon.OAuthRedirectURLs{
			RedirectURI: "http://localhost:7777/oauth2/callback",
			DaemonURL:   "http://localhost:7777/",
		},
		waitErr: context.Canceled,
	}
	daemonReceiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize Calendar")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	manualReceiver := oauth2flow.NewManualReceiver(strings.NewReader("manual-code\n"), daemonReceiver.RedirectURI())
	race := oauth2flow.NewRaceReceiver(daemonReceiver.RedirectURI(), daemonReceiver, manualReceiver)

	cfg := &oauth2.Config{
		ClientID: "client-id",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://provider.example.test/authorize",
			TokenURL: "https://provider.example.test/token",
		},
	}
	code, err := oauth2flow.AuthorizeCode(context.Background(), cfg, race, "", nil, func(string) {})
	if err != nil {
		t.Fatalf("AuthorizeCode(): %v", err)
	}
	if code != "manual-code" {
		t.Fatalf("code = %q, want manual-code", code)
	}
	if err := race.Close(); err != nil {
		t.Fatalf("race Close(): %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.begins) != 1 {
		t.Fatalf("begins = %#v, want daemon flow registered", client.begins)
	}
	if len(client.cancels) != 1 || client.cancels[0] != "flow-123" {
		t.Fatalf("cancels = %#v, want daemon flow canceled after manual wins", client.cancels)
	}
}

func TestDaemonOAuthReceiverManualWinsCancelsRealDaemonFlow(t *testing.T) {
	useShortDaemonDir(t)
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	udsServer, closeUDS := startTestDaemonServer(t)
	defer closeUDS()

	closeDebug, _, err := startDaemonDebugServer(io.Discard, nil, udsServer)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeDebug(); err != nil {
			t.Fatalf("close debug server: %v", err)
		}
	}()

	client, err := daemon.EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client Close(): %v", err)
		}
	}()

	daemonReceiver, err := newDaemonOAuthReceiver(context.Background(), client, "Authorize Calendar for workspace")
	if err != nil {
		t.Fatalf("newDaemonOAuthReceiver(): %v", err)
	}
	manualReceiver := oauth2flow.NewManualReceiver(strings.NewReader("manual-code\n"), daemonReceiver.RedirectURI())
	race := oauth2flow.NewRaceReceiver(daemonReceiver.RedirectURI(), daemonReceiver, manualReceiver)

	cfg := &oauth2.Config{
		ClientID: "client-id",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://provider.example.test/authorize",
			TokenURL: "https://provider.example.test/token",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, err := oauth2flow.AuthorizeCode(ctx, cfg, race, "", nil, func(string) {})
	if err != nil {
		t.Fatalf("AuthorizeCode(): %v", err)
	}
	if code != "manual-code" {
		t.Fatalf("code = %q, want manual-code", code)
	}
	if got := udsServer.PendingOAuthFlows(); len(got) != 1 {
		t.Fatalf("PendingOAuthFlows() before close = %#v, want one pending daemon flow", got)
	}
	if err := race.Close(); err != nil {
		t.Fatalf("race Close(): %v", err)
	}
	if got := udsServer.PendingOAuthFlows(); len(got) != 0 {
		t.Fatalf("PendingOAuthFlows() after manual-wins close = %#v, want none", got)
	}
}
