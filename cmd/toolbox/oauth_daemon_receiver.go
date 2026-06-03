package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/oauth2flow"
)

type daemonOAuthClient interface {
	OAuthRedirectURI(context.Context) (daemon.OAuthRedirectURLs, error)
	OAuthBegin(context.Context, daemon.OAuthBeginOptions) (daemon.OAuthFlow, error)
	OAuthWait(context.Context, string) (daemon.OAuthWaitResult, error)
	OAuthCancel(context.Context, string) error
}

type daemonOAuthReceiver struct {
	client      daemonOAuthClient
	redirectURI string
	daemonURL   string
	label       string

	mu     sync.Mutex
	flowID string
}

func newDaemonOAuthReceiver(ctx context.Context, client daemonOAuthClient, label string) (*daemonOAuthReceiver, error) {
	if client == nil {
		return nil, fmt.Errorf("daemon OAuth client is nil")
	}
	urls, err := client.OAuthRedirectURI(ctx)
	if err != nil {
		return nil, err
	}
	redirectURI := strings.TrimSpace(urls.RedirectURI)
	if err := validateDaemonOAuthURL("redirect_uri", redirectURI); err != nil {
		return nil, err
	}
	daemonURL := strings.TrimSpace(urls.DaemonURL)
	if err := validateDaemonOAuthURL("daemon_url", daemonURL); err != nil {
		return nil, err
	}
	return &daemonOAuthReceiver{
		client:      client,
		redirectURI: redirectURI,
		daemonURL:   daemonURL,
		label:       strings.TrimSpace(label),
	}, nil
}

func (r *daemonOAuthReceiver) RedirectURI() string {
	if r == nil {
		return ""
	}
	return r.redirectURI
}

func (r *daemonOAuthReceiver) DaemonURL() string {
	if r == nil {
		return ""
	}
	return r.daemonURL
}

func (r *daemonOAuthReceiver) FlowID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flowID
}

func (r *daemonOAuthReceiver) StartAuthorization(ctx context.Context, state string, authorizationURL string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("daemon OAuth receiver is unavailable")
	}
	r.mu.Lock()
	alreadyStarted := r.flowID != ""
	r.mu.Unlock()
	if alreadyStarted {
		return fmt.Errorf("daemon OAuth receiver already started")
	}
	flow, err := r.client.OAuthBegin(ctx, daemon.OAuthBeginOptions{
		State:            state,
		AuthorizationURL: authorizationURL,
		Label:            r.label,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(flow.FlowID) == "" {
		return fmt.Errorf("daemon OAuth begin returned empty flow_id")
	}
	r.mu.Lock()
	r.flowID = flow.FlowID
	r.mu.Unlock()
	return nil
}

func (r *daemonOAuthReceiver) ReceiveCode(ctx context.Context, expectedState string) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("daemon OAuth receiver is unavailable")
	}
	r.mu.Lock()
	flowID := r.flowID
	r.mu.Unlock()
	if strings.TrimSpace(flowID) == "" {
		return "", fmt.Errorf("daemon OAuth receiver has not started")
	}
	result, err := r.client.OAuthWait(ctx, flowID)
	if err != nil {
		return "", err
	}
	if expectedState != "" && result.State != "" && result.State != expectedState {
		return "", fmt.Errorf("oauth2flow: state mismatch (possible CSRF)")
	}
	if result.Error != "" {
		return "", oauth2flow.NewProviderAuthorizationError(result.Error, result.ErrorDescription, result.ErrorURI)
	}
	code := strings.TrimSpace(result.Code)
	if code == "" {
		return "", fmt.Errorf("daemon OAuth callback did not include an authorization code")
	}
	return code, nil
}

func (r *daemonOAuthReceiver) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	r.mu.Lock()
	flowID := r.flowID
	r.flowID = ""
	r.mu.Unlock()
	if strings.TrimSpace(flowID) == "" {
		return nil
	}
	return r.client.OAuthCancel(context.Background(), flowID)
}

func validateDaemonOAuthURL(field, raw string) error {
	if raw == "" {
		return fmt.Errorf("daemon OAuth %s is empty", field)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("daemon OAuth %s must be an absolute http:// or https:// URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("daemon OAuth %s must start with http:// or https://", field)
	}
	return nil
}
