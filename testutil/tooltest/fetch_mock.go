package tooltest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// FetchMock is a mock HTTP handler for testing tools that make fetch() calls.
// It implements http.RoundTripper for direct use with toolset.Config.FetchTransport.
//
// Register responses with HandleFunc or JSON. Matched requests are recorded for
// assertions via Calls(). Unmatched requests return 404.
//
// Example:
//
//	mock := tooltest.NewFetchMock()
//	mock.JSON("hn.algolia.com/api/v1/search", `{"hits":[...]}`)
//	prepared := tooltest.PrepareToolset(t, decl, toolset.Config{
//		FetchTransport: mock,
//	})
//	result, _ := invoke.Run(prepared, "stories.search", args)
//	if len(mock.Calls()) != 1 { t.Fatal("expected 1 call") }
type FetchMock struct {
	mu    sync.Mutex
	mux   *http.ServeMux
	calls []RecordedCall
}

// RecordedCall captures a request that matched a registered handler.
type RecordedCall struct {
	Method  string
	URL     string
	Host    string
	Path    string
	Headers http.Header
	Body    []byte
}

// NewFetchMock creates a new FetchMock.
func NewFetchMock() *FetchMock {
	return &FetchMock{mux: http.NewServeMux()}
}

// HandleFunc registers a handler for requests matching the pattern.
// Pattern uses http.ServeMux syntax and supports host matching:
// "hn.algolia.com/api/v1/search" matches that host + path.
// "example.com/" matches any path on example.com.
func (m *FetchMock) HandleFunc(pattern string, fn http.HandlerFunc) *FetchMock {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		m.record(r)
		fn(w, r)
	})
	return m
}

// JSON is a shortcut for a 200 OK JSON response.
func (m *FetchMock) JSON(pattern, body string) *FetchMock {
	return m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

// Status registers a handler returning a specific status code with empty body.
func (m *FetchMock) Status(pattern string, code int) *FetchMock {
	return m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	})
}

// Calls returns a copy of the recorded calls.
func (m *FetchMock) Calls() []RecordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// Reset clears recorded calls. Registered handlers are preserved.
func (m *FetchMock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// RoundTrip implements http.RoundTripper. Credentials and allowlist checks
// have already been applied by the toolbox transport layer before reaching here.
func (m *FetchMock) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	r2 := req.Clone(req.Context())
	if r2.Host == "" {
		r2.Host = req.URL.Host
	}
	m.mux.ServeHTTP(rec, r2)
	if req.Body != nil {
		_ = req.Body.Close()
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (m *FetchMock) record(r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, RecordedCall{
		Method:  r.Method,
		URL:     r.URL.String(),
		Host:    r.URL.Host,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    body,
	})
}
