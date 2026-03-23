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

	for _, entry := range req.headers.list {
		httpReq.Header.Add(entry[0], entry[1])
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 20 {
				return fmt.Errorf("fetch: too many redirects")
			}
			return nil
		},
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
