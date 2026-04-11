package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type SearchQuery struct {
	Q       string `json:"q"`
	Runtime string `json:"runtime,omitempty"`
	Effect  string `json:"effect,omitempty"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

type PackageSearchHit struct {
	ModulePath    string  `json:"modulePath"`
	Name          string  `json:"name"`
	Runtime       string  `json:"runtime"`
	Description   string  `json:"description"`
	LatestVersion string  `json:"latestVersion"`
	Rank          float64 `json:"rank"`
}

type ToolSearchHit struct {
	ModulePath     string  `json:"modulePath"`
	LatestVersion  string  `json:"latestVersion"`
	ToolPath       string  `json:"toolPath"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Effect         string  `json:"effect"`
	PackageName    string  `json:"packageName"`
	PackageRuntime string  `json:"packageRuntime"`
	Rank           float64 `json:"rank"`
}

type PackageSearchResponse struct {
	OK    bool               `json:"ok"`
	Hits  []PackageSearchHit `json:"hits"`
	Query SearchQuery        `json:"query"`
}

type ToolSearchResponse struct {
	OK    bool            `json:"ok"`
	Hits  []ToolSearchHit `json:"hits"`
	Query SearchQuery     `json:"query"`
}

type ToolRegistrySearchClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewToolRegistrySearchClient(baseURL string, client *http.Client) (*ToolRegistrySearchClient, error) {
	normalized, httpClient, err := normalizeToolRegistryClientConfig(baseURL, client)
	if err != nil {
		return nil, err
	}
	return &ToolRegistrySearchClient{
		baseURL:    normalized,
		httpClient: httpClient,
	}, nil
}

func (c *ToolRegistrySearchClient) SearchPackages(ctx context.Context, query SearchQuery) (PackageSearchResponse, error) {
	requestURL := c.searchURL("/v1/search", query)
	respBytes, err := c.getBytes(ctx, requestURL)
	if err != nil {
		return PackageSearchResponse{}, err
	}

	var response PackageSearchResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return PackageSearchResponse{}, fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}
	if !response.OK {
		return PackageSearchResponse{}, fmt.Errorf("GET %s: expected ok=true response", requestURL)
	}
	return response, nil
}

func (c *ToolRegistrySearchClient) SearchTools(ctx context.Context, query SearchQuery) (ToolSearchResponse, error) {
	requestURL := c.searchURL("/v1/tools/search", query)
	respBytes, err := c.getBytes(ctx, requestURL)
	if err != nil {
		return ToolSearchResponse{}, err
	}

	var response ToolSearchResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return ToolSearchResponse{}, fmt.Errorf("GET %s: decode response: %w", requestURL, err)
	}
	if !response.OK {
		return ToolSearchResponse{}, fmt.Errorf("GET %s: expected ok=true response", requestURL)
	}
	return response, nil
}

func (c *ToolRegistrySearchClient) searchURL(path string, query SearchQuery) string {
	values := url.Values{}
	values.Set("q", query.Q)
	if strings.TrimSpace(query.Runtime) != "" {
		values.Set("runtime", query.Runtime)
	}
	if strings.TrimSpace(query.Effect) != "" {
		values.Set("effect", query.Effect)
	}
	values.Set("limit", fmt.Sprintf("%d", query.Limit))
	values.Set("offset", fmt.Sprintf("%d", query.Offset))
	return c.baseURL + path + "?" + values.Encode()
}

func (c *ToolRegistrySearchClient) getBytes(ctx context.Context, requestURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", requestURL, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read response body: %w", requestURL, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyToolRegistrySearchHTTPError(requestURL, resp.StatusCode, body)
	}
	return body, nil
}

func classifyToolRegistrySearchHTTPError(requestURL string, statusCode int, body []byte) error {
	detail := parseToolRegistryErrorDetail(body)
	if statusCode == http.StatusBadRequest && detail != "" {
		return fmt.Errorf("%s", detail)
	}
	if detail == "" {
		detail = http.StatusText(statusCode)
	}
	return fmt.Errorf("GET %s: unexpected status %d: %s", requestURL, statusCode, detail)
}

func parseToolRegistryErrorDetail(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	return strings.TrimSpace(string(body))
}
