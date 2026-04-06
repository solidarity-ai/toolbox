package tooltest_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/testutil/tooltest"
)

func TestFetchMock_JSON(t *testing.T) {
	mock := tooltest.NewFetchMock().JSON("example.com/ping", `{"ok":true}`)
	client := &http.Client{Transport: mock}

	resp, err := client.Get("https://example.com/ping")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Method != "GET" || calls[0].Host != "example.com" || calls[0].Path != "/ping" {
		t.Errorf("call = %+v", calls[0])
	}
}

func TestFetchMock_HandleFunc_RecordsBodyAndHeaders(t *testing.T) {
	mock := tooltest.NewFetchMock().HandleFunc("api.example.com/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("created"))
	})
	client := &http.Client{Transport: mock}

	req, _ := http.NewRequest("POST", "https://api.example.com/echo", strings.NewReader(`{"x":1}`))
	req.Header.Set("X-Custom", "abc")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("status = %d", resp.StatusCode)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Headers.Get("X-Custom") != "abc" {
		t.Errorf("header X-Custom = %q", calls[0].Headers.Get("X-Custom"))
	}
	if string(calls[0].Body) != `{"x":1}` {
		t.Errorf("body = %q", calls[0].Body)
	}
}

func TestFetchMock_Status(t *testing.T) {
	mock := tooltest.NewFetchMock().Status("example.com/", 503)
	client := &http.Client{Transport: mock}
	resp, err := client.Get("https://example.com/anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestFetchMock_Unmatched404(t *testing.T) {
	mock := tooltest.NewFetchMock()
	client := &http.Client{Transport: mock}
	resp, err := client.Get("https://nowhere.example.com/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if len(mock.Calls()) != 0 {
		t.Errorf("unmatched should not record calls")
	}
}

func TestFetchMock_Reset(t *testing.T) {
	mock := tooltest.NewFetchMock().JSON("example.com/ping", `{}`)
	client := &http.Client{Transport: mock}
	resp, _ := client.Get("https://example.com/ping")
	resp.Body.Close()
	if len(mock.Calls()) != 1 {
		t.Fatalf("expected 1 call before reset")
	}
	mock.Reset()
	if len(mock.Calls()) != 0 {
		t.Fatalf("expected 0 calls after reset")
	}
	// handler still registered
	resp, _ = client.Get("https://example.com/ping")
	resp.Body.Close()
	if len(mock.Calls()) != 1 {
		t.Fatalf("handler should persist after reset")
	}
}
