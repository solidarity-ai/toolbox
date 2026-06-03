//go:build !windows

package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"github.com/solidarity-ai/toolbox/daemon/internal/processctl"
	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

const (
	dialTimeout      = 500 * time.Millisecond
	launchPollDelay  = 50 * time.Millisecond
	launchPollMaxAge = 5 * time.Second
)

func EnsureConnection() (*Client, error) {
	sockPath, err := daemonpaths.SocketPath()
	if err != nil {
		return nil, err
	}
	if client, err := dialAndPing(sockPath); err == nil {
		return client, nil
	}

	if err := processctl.EnsureRuntimeDir(); err != nil {
		return nil, err
	}

	lockFile, err := processctl.AcquireLaunchLock()
	if err != nil {
		return nil, err
	}
	defer func() { _ = processctl.ReleaseLaunchLock(lockFile) }()

	if client, err := dialAndPing(sockPath); err == nil {
		return client, nil
	}

	pid, pidErr := processctl.ReadPID()
	switch {
	case pidErr == nil && !processctl.IsProcessAlive(pid):
		if err := processctl.CleanStale(); err != nil {
			return nil, err
		}
	case pidErr != nil && !errors.Is(pidErr, os.ErrNotExist):
		if err := processctl.CleanStale(); err != nil {
			return nil, err
		}
	case errors.Is(pidErr, os.ErrNotExist):
		if err := processctl.CleanStale(); err != nil {
			return nil, err
		}
	}

	if err := processctl.Launch(); err != nil {
		return nil, err
	}
	client, err := waitForDaemon(sockPath)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	c.httpClient = nil
	c.sessionService = nil
	c.secretStoreService = nil
	c.oauthService = nil
	return nil
}

func (c *Client) reconnectLocked() error {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	replacement := newClient(c.socketPath)
	c.httpClient = replacement.httpClient
	c.sessionService = replacement.sessionService
	c.secretStoreService = replacement.secretStoreService
	c.oauthService = replacement.oauthService
	return nil
}

func dialAndPing(sockPath string) (*Client, error) {
	client := newClient(sockPath)
	resp, err := pingService(client.sessionService)
	if err != nil {
		return nil, err
	}
	if resp.Payload != "pong" {
		return nil, fmt.Errorf("unexpected daemon ping response: %q", resp.Payload)
	}
	return client, nil
}

func waitForDaemon(sockPath string) (*Client, error) {
	deadline := time.Now().Add(launchPollMaxAge)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := dialAndPing(sockPath)
		if err == nil {
			return client, nil
		}
		lastErr = err
		time.Sleep(launchPollDelay)
	}
	if lastErr == nil {
		lastErr = errors.New("daemon did not become ready")
	}
	return nil, fmt.Errorf("wait for daemon readiness: %w", lastErr)
}

func (c *Client) Ping() (PingResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sessionService == nil {
		if err := c.reconnectLocked(); err != nil {
			return PingResult{}, err
		}
	}

	resp, err := pingService(c.sessionService)
	if err == nil {
		return resp, nil
	}
	if err := c.reconnectLocked(); err != nil {
		return PingResult{}, fmt.Errorf("reconnect after daemon ping failed: %w", err)
	}
	return pingService(c.sessionService)
}

func newClient(socketPath string) *Client {
	httpClient := transport.NewUnixHTTPClient(socketPath)
	return &Client{
		socketPath:         socketPath,
		httpClient:         httpClient,
		sessionService:     daemonv1connect.NewSessionServiceClient(httpClient, sessionServiceBaseURL),
		secretStoreService: daemonv1connect.NewSecretStoreServiceClient(httpClient, sessionServiceBaseURL),
		oauthService:       daemonv1connect.NewOAuthServiceClient(httpClient, sessionServiceBaseURL),
	}
}

func pingService(service daemonv1connect.SessionServiceClient) (PingResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	resp, err := service.Ping(ctx, connect.NewRequest(&daemonv1.PingRequest{}))
	if err != nil {
		return PingResult{}, err
	}
	return PingResult{
		Payload: resp.Msg.GetPayload(),
		PID:     int(resp.Msg.GetPid()),
	}, nil
}
