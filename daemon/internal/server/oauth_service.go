package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const OAuthHostEnv = "TOOLBOX_HOST"

type OAuthService struct {
	daemonv1connect.UnimplementedOAuthServiceHandler

	mu             sync.RWMutex
	webserverAddr  string
	toolboxHostEnv func() string
	flows          *oauthFlowManager
}

func NewOAuthService() *OAuthService {
	return &OAuthService{
		toolboxHostEnv: func() string {
			return os.Getenv(OAuthHostEnv)
		},
		flows: newOAuthFlowManager(defaultOAuthFlowTTL),
	}
}

func (s *OAuthService) Handler(opts ...connect.HandlerOption) (string, http.Handler) {
	return daemonv1connect.NewOAuthServiceHandler(s, opts...)
}

func (s *OAuthService) SetWebserverAddress(addr string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webserverAddr = strings.TrimSpace(addr)
}

func (s *OAuthService) RedirectURI(context.Context, *connect.Request[daemonv1.OAuthRedirectURIRequest]) (*connect.Response[daemonv1.OAuthRedirectURIResponse], error) {
	redirectURI, daemonURL, err := s.redirectURLs()
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&daemonv1.OAuthRedirectURIResponse{
		RedirectUri: redirectURI,
		DaemonUrl:   daemonURL,
	}), nil
}

func (s *OAuthService) Begin(_ context.Context, req *connect.Request[daemonv1.OAuthBeginRequest]) (*connect.Response[daemonv1.OAuthBeginResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, oauthFlowConnectError(newOAuthFlowError(oauthFlowErrorInvalid, "OAuth begin request is required"))
	}
	flow, err := s.flows.Begin(req.Msg.GetState(), req.Msg.GetAuthorizationUrl(), req.Msg.GetLabel())
	if err != nil {
		return nil, oauthFlowConnectError(err)
	}
	return connect.NewResponse(&daemonv1.OAuthBeginResponse{
		FlowId:    flow.flowID,
		ExpiresAt: timestamppb.New(flow.expiresAt),
	}), nil
}

func (s *OAuthService) Wait(ctx context.Context, req *connect.Request[daemonv1.OAuthWaitRequest]) (*connect.Response[daemonv1.OAuthWaitResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, oauthFlowConnectError(newOAuthFlowError(oauthFlowErrorInvalid, "OAuth wait request is required"))
	}
	result, err := s.flows.Wait(ctx, req.Msg.GetFlowId())
	if err != nil {
		return nil, oauthFlowConnectError(err)
	}
	return connect.NewResponse(&daemonv1.OAuthWaitResponse{
		Code:             result.Code,
		State:            result.State,
		Error:            result.Error,
		ErrorDescription: result.ErrorDescription,
		ErrorUri:         result.ErrorURI,
	}), nil
}

func (s *OAuthService) Cancel(_ context.Context, req *connect.Request[daemonv1.OAuthCancelRequest]) (*connect.Response[daemonv1.OAuthCancelResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, oauthFlowConnectError(newOAuthFlowError(oauthFlowErrorInvalid, "OAuth cancel request is required"))
	}
	if err := s.flows.Cancel(req.Msg.GetFlowId()); err != nil {
		return nil, oauthFlowConnectError(err)
	}
	return connect.NewResponse(&daemonv1.OAuthCancelResponse{}), nil
}

func (s *OAuthService) Close() {
	if s == nil || s.flows == nil {
		return
	}
	s.flows.Close()
}

func (s *OAuthService) CompleteOAuthFlow(state string, result OAuthCallbackResult) error {
	if s == nil || s.flows == nil {
		return newOAuthFlowError(oauthFlowErrorUnavailable, "daemon OAuth service is unavailable")
	}
	return s.flows.Complete(state, result)
}

func (s *OAuthService) PendingOAuthFlows() []OAuthFlowSnapshot {
	if s == nil || s.flows == nil {
		return nil
	}
	return s.flows.Pending()
}

func (s *OAuthService) redirectURLs() (string, string, error) {
	if s == nil {
		return "", "", errors.New("daemon OAuth service is unavailable")
	}
	s.mu.RLock()
	addr := s.webserverAddr
	getenv := s.toolboxHostEnv
	s.mu.RUnlock()

	host := ""
	if getenv != nil {
		host = strings.TrimSpace(getenv())
	}
	if host != "" {
		base, err := oauthBaseURLFromToolboxHost(host)
		if err != nil {
			return "", "", err
		}
		return oauthAppendPath(base, "oauth2/callback"), oauthAppendPath(base, ""), nil
	}
	if strings.TrimSpace(addr) == "" {
		return "", "", errors.New("daemon OAuth webserver is unavailable")
	}
	base, err := oauthBaseURLFromListenerAddress(addr)
	if err != nil {
		return "", "", err
	}
	return oauthAppendPath(base, "oauth2/callback"), oauthAppendPath(base, ""), nil
}

func oauthBaseURLFromToolboxHost(raw string) (*url.URL, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return nil, errors.New("TOOLBOX_HOST is empty")
	}
	base, err := url.Parse(trimmed)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("TOOLBOX_HOST must be an absolute http:// or https:// URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, errors.New("TOOLBOX_HOST must start with http:// or https://")
	}
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	return base, nil
}

func oauthBaseURLFromListenerAddress(addr string) (*url.URL, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || strings.TrimSpace(port) == "" {
		return nil, errors.New("daemon OAuth webserver address is invalid")
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
		host = "localhost"
	}
	return &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, port),
	}, nil
}

func oauthAppendPath(base *url.URL, suffix string) string {
	u := *base
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	prefix := strings.TrimRight(u.Path, "/")
	if suffix == "" {
		if prefix == "" {
			u.Path = "/"
		} else {
			u.Path = prefix + "/"
		}
		return u.String()
	}
	if prefix == "" {
		u.Path = "/" + suffix
	} else {
		u.Path = prefix + "/" + suffix
	}
	return u.String()
}
