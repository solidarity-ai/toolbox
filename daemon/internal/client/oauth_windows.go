package client

import (
	"context"
	"time"
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

func (c *Client) OAuthRedirectURI(context.Context) (OAuthRedirectURLs, error) {
	return OAuthRedirectURLs{}, ErrUnsupportedPlatform
}

func (c *Client) OAuthBegin(context.Context, OAuthBeginOptions) (OAuthFlow, error) {
	return OAuthFlow{}, ErrUnsupportedPlatform
}

func (c *Client) OAuthWait(context.Context, string) (OAuthCallbackResult, error) {
	return OAuthCallbackResult{}, ErrUnsupportedPlatform
}

func (c *Client) OAuthCancel(context.Context, string) error {
	return ErrUnsupportedPlatform
}
