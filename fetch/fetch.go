package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fetch performs an HTTP request following the Fetch API semantics.
// It accepts a URL string and optional RequestInit.
func Fetch(ctx context.Context, url string, init *RequestInit) (*Response, error) {
	req := NewRequest(url, init)

	var body io.Reader
	if req.body != nil {
		body = req.body
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, body)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	// Set default headers per the Fetch spec.
	hasAccept, hasAcceptLang := false, false
	for _, entry := range req.headers.list {
		httpReq.Header.Add(entry[0], entry[1])
		switch entry[0] {
		case "accept":
			hasAccept = true
		case "accept-language":
			hasAcceptLang = true
		}
	}
	if !hasAccept {
		httpReq.Header.Set("Accept", "*/*")
	}
	if !hasAcceptLang {
		httpReq.Header.Set("Accept-Language", "*")
	}

	var prepareRequest func(*http.Request, []*http.Request) error
	if init != nil && init.PrepareRequest != nil {
		prepareRequest = init.PrepareRequest
	}

	if prepareRequest != nil {
		if err := prepareRequest(httpReq, nil); err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}
	}

	// Capture the optional caller-supplied redirect check.
	var extraRedirectCheck func(*http.Request, []*http.Request) error
	if init != nil && init.CheckRedirect != nil {
		extraRedirectCheck = init.CheckRedirect
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 20 {
				return fmt.Errorf("fetch: too many redirects")
			}
			if shouldStripSensitiveHeadersOnRedirect(req, via) {
				req.Header.Del("Authorization")
				req.Header.Del("X-API-Key")
			}
			if prepareRequest != nil {
				if err := prepareRequest(req, via); err != nil {
					return err
				}
			}
			if extraRedirectCheck != nil {
				return extraRedirectCheck(req, via)
			}
			return nil
		},
	}
	if init != nil && init.Transport != nil {
		client.Transport = init.Transport
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	respHeaders := NewHeadersFromHTTP(httpResp.Header)

	resp := &Response{
		status:     httpResp.StatusCode,
		statusText: strings.TrimPrefix(httpResp.Status, fmt.Sprintf("%d ", httpResp.StatusCode)),
		headers:    respHeaders,
		body:       httpResp.Body,
		url:        httpResp.Request.URL.String(),
		ok:         httpResp.StatusCode >= 200 && httpResp.StatusCode < 300,
		redirected: httpResp.Request.URL.String() != url,
	}

	return resp, nil
}

func shouldStripSensitiveHeadersOnRedirect(req *http.Request, via []*http.Request) bool {
	if len(via) == 0 {
		return false
	}
	if req.URL.Host != via[0].URL.Host {
		return true
	}
	prev := via[len(via)-1]
	return strings.EqualFold(prev.URL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http")
}
