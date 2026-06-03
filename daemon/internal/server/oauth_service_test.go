package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

func TestOAuthBeginWaitCompletesThroughConnect(t *testing.T) {
	_, srv, client := startOAuthConnectServer(t)

	flow := beginOAuthFlow(t, client, "state-complete", "https://provider.example/authorize?client_id=test")
	waitObserved := observeOAuthWaitLookup(srv.oauthService.flows, srv.oauthService.flows.now)
	waitResult := make(chan *daemonv1.OAuthWaitResponse, 1)
	waitErr := make(chan error, 1)
	go func() {
		resp, err := client.Wait(context.Background(), connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: flow.GetFlowId()}))
		if err != nil {
			waitErr <- err
			return
		}
		waitResult <- resp.Msg
	}()

	waitForOAuthWaiters(t, waitObserved, 1)
	if err := srv.CompleteOAuthFlow("state-complete", OAuthCallbackResult{Code: "code-123"}); err != nil {
		t.Fatalf("CompleteOAuthFlow(): %v", err)
	}

	select {
	case err := <-waitErr:
		t.Fatalf("Wait(): %v", err)
	case result := <-waitResult:
		if result.GetCode() != "code-123" || result.GetState() != "state-complete" {
			t.Fatalf("Wait() = %#v, want code-123/state-complete", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OAuth wait result")
	}
	if err := srv.CompleteOAuthFlow("state-complete", OAuthCallbackResult{Code: "second-code"}); err == nil {
		t.Fatal("CompleteOAuthFlow(consumed state) succeeded, want error")
	}
}

func TestOAuthCancelUnblocksWaiterAndRemovesStateThroughConnect(t *testing.T) {
	_, srv, client := startOAuthConnectServer(t)

	flow := beginOAuthFlow(t, client, "state-cancel", "https://provider.example/authorize")
	waitObserved := observeOAuthWaitLookup(srv.oauthService.flows, srv.oauthService.flows.now)
	waitErr := make(chan error, 1)
	go func() {
		_, err := client.Wait(context.Background(), connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: flow.GetFlowId()}))
		waitErr <- err
	}()

	waitForOAuthWaiters(t, waitObserved, 1)
	if _, err := client.Cancel(context.Background(), connect.NewRequest(&daemonv1.OAuthCancelRequest{FlowId: flow.GetFlowId()})); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	if _, err := client.Cancel(context.Background(), connect.NewRequest(&daemonv1.OAuthCancelRequest{FlowId: flow.GetFlowId()})); err != nil {
		t.Fatalf("Cancel(idempotent): %v", err)
	}

	select {
	case err := <-waitErr:
		assertConnectCode(t, err, connect.CodeCanceled)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OAuth wait cancellation")
	}
	if err := srv.CompleteOAuthFlow("state-cancel", OAuthCallbackResult{Code: "late-code"}); err == nil {
		t.Fatal("CompleteOAuthFlow(canceled state) succeeded, want error")
	}
	_, err := client.Wait(context.Background(), connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: flow.GetFlowId()}))
	assertConnectCode(t, err, connect.CodeNotFound)
}

func TestOAuthMultipleFlowsCompleteIndependentlyThroughConnect(t *testing.T) {
	_, srv, client := startOAuthConnectServer(t)

	first := beginOAuthFlow(t, client, "state-one", "https://provider.example/authorize?flow=one")
	second := beginOAuthFlow(t, client, "state-two", "https://provider.example/authorize?flow=two")
	if first.GetFlowId() == second.GetFlowId() {
		t.Fatalf("flow IDs are equal: %q", first.GetFlowId())
	}

	type waitOutcome struct {
		name string
		msg  *daemonv1.OAuthWaitResponse
		err  error
	}
	waitObserved := observeOAuthWaitLookup(srv.oauthService.flows, srv.oauthService.flows.now)
	outcomes := make(chan waitOutcome, 2)
	go func() {
		resp, err := client.Wait(context.Background(), connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: first.GetFlowId()}))
		outcome := waitOutcome{name: "first", err: err}
		if resp != nil {
			outcome.msg = resp.Msg
		}
		outcomes <- outcome
	}()
	go func() {
		resp, err := client.Wait(context.Background(), connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: second.GetFlowId()}))
		outcome := waitOutcome{name: "second", err: err}
		if resp != nil {
			outcome.msg = resp.Msg
		}
		outcomes <- outcome
	}()

	waitForOAuthWaiters(t, waitObserved, 2)
	if err := srv.CompleteOAuthFlow("state-two", OAuthCallbackResult{Code: "code-two"}); err != nil {
		t.Fatalf("CompleteOAuthFlow(second): %v", err)
	}
	if err := srv.CompleteOAuthFlow("state-one", OAuthCallbackResult{Code: "code-one"}); err != nil {
		t.Fatalf("CompleteOAuthFlow(first): %v", err)
	}

	got := map[string]*daemonv1.OAuthWaitResponse{}
	for range 2 {
		select {
		case outcome := <-outcomes:
			if outcome.err != nil {
				t.Fatalf("Wait(%s): %v", outcome.name, outcome.err)
			}
			got[outcome.name] = outcome.msg
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for OAuth wait result")
		}
	}
	if got["first"].GetCode() != "code-one" || got["first"].GetState() != "state-one" {
		t.Fatalf("first result = %#v, want code-one/state-one", got["first"])
	}
	if got["second"].GetCode() != "code-two" || got["second"].GetState() != "state-two" {
		t.Fatalf("second result = %#v, want code-two/state-two", got["second"])
	}
}

func TestOAuthBeginValidatesRequestsThroughConnect(t *testing.T) {
	_, _, client := startOAuthConnectServer(t)

	_, err := client.Begin(context.Background(), connect.NewRequest(&daemonv1.OAuthBeginRequest{
		AuthorizationUrl: "https://provider.example/authorize",
	}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	_, err = client.Begin(context.Background(), connect.NewRequest(&daemonv1.OAuthBeginRequest{
		State:            "state-invalid-url",
		AuthorizationUrl: "not-a-url",
	}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	_, err = client.Begin(context.Background(), connect.NewRequest(&daemonv1.OAuthBeginRequest{
		State:            "state-relative-url",
		AuthorizationUrl: "/authorize",
	}))
	assertConnectCode(t, err, connect.CodeInvalidArgument)

	beginOAuthFlow(t, client, "state-duplicate", "https://provider.example/authorize")
	_, err = client.Begin(context.Background(), connect.NewRequest(&daemonv1.OAuthBeginRequest{
		State:            "state-duplicate",
		AuthorizationUrl: "https://provider.example/authorize?again=1",
	}))
	assertConnectCode(t, err, connect.CodeAlreadyExists)
}

func TestOAuthCompleteUnknownStateDoesNotCreateFlow(t *testing.T) {
	_, srv, client := startOAuthConnectServer(t)

	if err := srv.CompleteOAuthFlow("never-registered", OAuthCallbackResult{Code: "code"}); err == nil {
		t.Fatal("CompleteOAuthFlow(unknown state) succeeded, want error")
	}

	flow := beginOAuthFlow(t, client, "never-registered", "https://provider.example/authorize")
	if flow.GetFlowId() == "" {
		t.Fatal("Begin(after unknown callback) returned empty flow ID")
	}
}

func startOAuthConnectServer(t *testing.T) (string, *Server, daemonv1connect.OAuthServiceClient) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(daemonpaths.DaemonDirEnv, dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	socketPath, err := daemonpaths.SocketPath()
	if err != nil {
		t.Fatalf("SocketPath(): %v", err)
	}
	pidPath, err := daemonpaths.PIDPath()
	if err != nil {
		t.Fatalf("PIDPath(): %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	srv := NewServer(listener)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Serve(): %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for OAuth Connect server shutdown")
		}
	})

	httpClient := transport.NewUnixHTTPClient(socketPath)
	t.Cleanup(httpClient.CloseIdleConnections)
	return socketPath, srv, daemonv1connect.NewOAuthServiceClient(httpClient, "http://toolbox-daemon")
}

func beginOAuthFlow(t *testing.T, client daemonv1connect.OAuthServiceClient, state, authorizationURL string) *daemonv1.OAuthBeginResponse {
	t.Helper()
	resp, err := client.Begin(context.Background(), connect.NewRequest(&daemonv1.OAuthBeginRequest{
		State:            state,
		AuthorizationUrl: authorizationURL,
		Label:            "Authorize test credential",
	}))
	if err != nil {
		t.Fatalf("Begin(%q): %v", state, err)
	}
	if resp.Msg.GetFlowId() == "" {
		t.Fatalf("Begin(%q) returned empty flow ID", state)
	}
	if resp.Msg.GetExpiresAt() == nil || resp.Msg.GetExpiresAt().AsTime().IsZero() {
		t.Fatalf("Begin(%q) returned zero expires_at", state)
	}
	return resp.Msg
}

func waitForOAuthWaiters(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d OAuth waiters", count)
		}
	}
}

func assertConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want connect code %v", want)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %T %[1]v, want connect error code %v", err, want)
	}
	if connectErr.Code() != want {
		t.Fatalf("connect code = %v, want %v (error %v)", connectErr.Code(), want, err)
	}
}
