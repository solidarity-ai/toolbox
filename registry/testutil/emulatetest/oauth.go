package emulatetest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

var (
	googleAuthorizeFormActionRe = regexp.MustCompile(`<form[^>]*method="post"[^>]*action="([^"]+)"`)
	googleHiddenInputRe         = regexp.MustCompile(`<input[^>]*type="hidden"[^>]*name="([^"]+)"[^>]*value="([^"]*)"/?>`)
)

type GoogleOIDCDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

func (s *Server) GoogleDiscovery(ctx context.Context) (GoogleOIDCDiscovery, error) {
	if s == nil {
		return GoogleOIDCDiscovery{}, fmt.Errorf("google discovery requires a running emulate server")
	}
	if s.Service() != "google" {
		return GoogleOIDCDiscovery{}, fmt.Errorf("google discovery requires google emulate service, got %q", s.Service())
	}
	requestURL := strings.TrimRight(s.BaseURL(), "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return GoogleOIDCDiscovery{}, fmt.Errorf("build google discovery request: %w", err)
	}
	resp, err := s.RawClient().Do(req)
	if err != nil {
		return GoogleOIDCDiscovery{}, fmt.Errorf("fetch google discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return GoogleOIDCDiscovery{}, fmt.Errorf("fetch google discovery returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var discovery GoogleOIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return GoogleOIDCDiscovery{}, fmt.Errorf("decode google discovery: %w", err)
	}
	if strings.TrimSpace(discovery.AuthorizationEndpoint) == "" || strings.TrimSpace(discovery.TokenEndpoint) == "" {
		return GoogleOIDCDiscovery{}, fmt.Errorf("google discovery missing authorization or token endpoint")
	}
	return discovery, nil
}

func (s *Server) GoogleProviderRef(ctx context.Context) (tooldef.OAuth2ProviderRef, error) {
	discovery, err := s.GoogleDiscovery(ctx)
	if err != nil {
		return tooldef.OAuth2ProviderRef{}, err
	}
	secureBase, err := url.Parse(strings.TrimRight(s.SecureURL(), "/"))
	if err != nil {
		return tooldef.OAuth2ProviderRef{}, fmt.Errorf("parse google emulate secure url %q: %w", s.SecureURL(), err)
	}
	if secureBase.Scheme == "" || secureBase.Host == "" {
		return tooldef.OAuth2ProviderRef{}, fmt.Errorf("google emulate secure url is unavailable")
	}
	rewrite := func(raw string) (string, error) {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("parse discovery endpoint %q: %w", raw, err)
		}
		parsed.Scheme = secureBase.Scheme
		parsed.Host = secureBase.Host
		return parsed.String(), nil
	}
	authURL, err := rewrite(discovery.AuthorizationEndpoint)
	if err != nil {
		return tooldef.OAuth2ProviderRef{}, err
	}
	tokenURL, err := rewrite(discovery.TokenEndpoint)
	if err != nil {
		return tooldef.OAuth2ProviderRef{}, err
	}
	return tooldef.OAuth2ProviderRef{Endpoints: &tooldef.OAuth2ProviderEndpoints{
		AuthURL:  authURL,
		TokenURL: tokenURL,
	}}, nil
}

func (s *Server) CompleteGoogleAuthorization(ctx context.Context, authURL string) error {
	if s == nil {
		return fmt.Errorf("google authorization requires a running emulate server")
	}
	if s.Service() != "google" {
		return fmt.Errorf("google authorization requires google emulate service, got %q", s.Service())
	}

	pageReq, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return fmt.Errorf("build google authorize-page request: %w", err)
	}
	pageResp, err := s.SecureClient().Do(pageReq)
	if err != nil {
		return fmt.Errorf("load google authorize page: %w", err)
	}
	pageBody, readErr := io.ReadAll(pageResp.Body)
	pageResp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read google authorize page: %w", readErr)
	}
	if pageResp.StatusCode != http.StatusOK {
		return fmt.Errorf("load google authorize page returned status %d: %s", pageResp.StatusCode, strings.TrimSpace(string(pageBody)))
	}

	formAction, formValues, err := parseGoogleAuthorizeForm(string(pageBody))
	if err != nil {
		return err
	}
	postURL, err := url.Parse(formAction)
	if err != nil {
		return fmt.Errorf("parse google authorize form action %q: %w", formAction, err)
	}
	if !postURL.IsAbs() {
		baseURL, err := url.Parse(authURL)
		if err != nil {
			return fmt.Errorf("parse google authorize url %q: %w", authURL, err)
		}
		postURL = baseURL.ResolveReference(postURL)
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL.String(), strings.NewReader(formValues.Encode()))
	if err != nil {
		return fmt.Errorf("build google authorize submit request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.SecureClient().Do(postReq)
	if err != nil {
		return fmt.Errorf("submit google authorize form: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read google authorize submit response: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("complete google authorization callback returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(string(body), "Authorization received. You can close this window.") {
		return fmt.Errorf("complete google authorization callback returned unexpected body: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func parseGoogleAuthorizeForm(body string) (string, url.Values, error) {
	actionMatch := googleAuthorizeFormActionRe.FindStringSubmatch(body)
	if len(actionMatch) != 2 || strings.TrimSpace(actionMatch[1]) == "" {
		return "", nil, fmt.Errorf("google authorize page missing post form action")
	}
	fields := url.Values{}
	for _, match := range googleHiddenInputRe.FindAllStringSubmatch(body, -1) {
		if len(match) != 3 {
			continue
		}
		fields.Set(match[1], match[2])
	}
	requiredFields := []string{"email", "redirect_uri", "scope", "state", "client_id", "code_challenge", "code_challenge_method"}
	for _, name := range requiredFields {
		if _, ok := fields[name]; !ok {
			return "", nil, fmt.Errorf("google authorize page missing hidden input %q", name)
		}
	}
	if !strings.Contains(body, "class=\"user-btn\"") {
		return "", nil, fmt.Errorf("google authorize page missing user submit button")
	}
	return actionMatch[1], fields, nil
}
