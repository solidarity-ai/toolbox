//go:build !windows

package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
)

type OAuthRedirectURLs struct {
	RedirectURI string
	DaemonURL   string
}

type OAuthBeginOptions struct {
	State            string
	AuthorizationURL string
	Label            string
}

type OAuthFlow struct {
	FlowID    string
	ExpiresAt time.Time
}

type OAuthCallbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
	ErrorURI         string
}

func (c *Client) OAuthRedirectURI(ctx context.Context) (OAuthRedirectURLs, error) {
	var urls OAuthRedirectURLs
	err := c.withOAuthService(ctx, func(service daemonv1connect.OAuthServiceClient) error {
		resp, err := service.RedirectURI(ctx, connect.NewRequest(&daemonv1.OAuthRedirectURIRequest{}))
		if err != nil {
			return err
		}
		urls = OAuthRedirectURLs{
			RedirectURI: resp.Msg.GetRedirectUri(),
			DaemonURL:   resp.Msg.GetDaemonUrl(),
		}
		return nil
	})
	return urls, err
}

func (c *Client) OAuthBegin(ctx context.Context, opts OAuthBeginOptions) (OAuthFlow, error) {
	var flow OAuthFlow
	err := c.withOAuthService(ctx, func(service daemonv1connect.OAuthServiceClient) error {
		resp, err := service.Begin(ctx, connect.NewRequest(&daemonv1.OAuthBeginRequest{
			State:            opts.State,
			AuthorizationUrl: opts.AuthorizationURL,
			Label:            opts.Label,
		}))
		if err != nil {
			return err
		}
		flow = OAuthFlow{
			FlowID: resp.Msg.GetFlowId(),
		}
		if expiresAt := resp.Msg.GetExpiresAt(); expiresAt != nil {
			flow.ExpiresAt = expiresAt.AsTime()
		}
		return nil
	})
	return flow, err
}

func (c *Client) OAuthWait(ctx context.Context, flowID string) (OAuthCallbackResult, error) {
	var result OAuthCallbackResult
	err := c.withOAuthService(ctx, func(service daemonv1connect.OAuthServiceClient) error {
		resp, err := service.Wait(ctx, connect.NewRequest(&daemonv1.OAuthWaitRequest{FlowId: flowID}))
		if err != nil {
			return err
		}
		result = OAuthCallbackResult{
			Code:             resp.Msg.GetCode(),
			State:            resp.Msg.GetState(),
			Error:            resp.Msg.GetError(),
			ErrorDescription: resp.Msg.GetErrorDescription(),
			ErrorURI:         resp.Msg.GetErrorUri(),
		}
		return nil
	})
	return result, err
}

func (c *Client) OAuthCancel(ctx context.Context, flowID string) error {
	return c.withOAuthService(ctx, func(service daemonv1connect.OAuthServiceClient) error {
		_, err := service.Cancel(ctx, connect.NewRequest(&daemonv1.OAuthCancelRequest{FlowId: flowID}))
		return err
	})
}

func (c *Client) withOAuthService(ctx context.Context, fn func(daemonv1connect.OAuthServiceClient) error) error {
	c.mu.Lock()

	if c.oauthService == nil {
		if err := c.reconnectLocked(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	service := c.oauthService
	c.mu.Unlock()

	if err := fn(service); err == nil {
		return nil
	} else {
		if ctx.Err() != nil || !shouldRetryOAuthRequest(err) {
			return err
		}
		c.mu.Lock()
		if reconnectErr := c.reconnectLocked(); reconnectErr != nil {
			c.mu.Unlock()
			return fmt.Errorf("reconnect after daemon OAuth request failed: %w (reconnect: %v)", err, reconnectErr)
		}
		service = c.oauthService
		c.mu.Unlock()
		if retryErr := fn(service); retryErr != nil {
			return retryErr
		}
		return nil
	}
}

func shouldRetryOAuthRequest(err error) bool {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return true
	}
	return connectErr.Code() == connect.CodeUnavailable
}
