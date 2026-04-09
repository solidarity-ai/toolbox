package vcr

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Authorization", "Bearer leaked-server-token-abcdefghijklmnop")
		_, _ = w.Write([]byte(body))
	}))
}

func TestRecordWritesCassette(t *testing.T) {
	srv := newTestServer(t, map[string]string{"/ping": `{"ok":true}`})
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "rec.json")
	r, err := NewRecorder(Config{Mode: ModeRecord, Path: path, StripHost: strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: r}
	req, _ := http.NewRequest("GET", srv.URL+"/ping", nil)
	req.Header.Set("Authorization", "Bearer client-secret-abcdefghijklmnop")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "ok") {
		t.Errorf("body=%q", body)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := LoadCassette(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Episodes) != 1 {
		t.Fatalf("episodes=%d", len(raw.Episodes))
	}
	ep := raw.Episodes[0]
	if ep.Request.URL != "/ping" {
		t.Errorf("url=%q want /ping (stripped)", ep.Request.URL)
	}
	if got := ep.Request.Headers["Authorization"]; len(got) == 0 || got[0] != "REDACTED" {
		t.Errorf("request Authorization not redacted: %v", got)
	}
	if got := ep.Response.Headers["Authorization"]; len(got) == 0 || got[0] != "REDACTED" {
		t.Errorf("response Authorization not redacted: %v", got)
	}
}

func TestReplayReadsCassette(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rep.json")
	cass := &Cassette{
		Version: 1,
		Episodes: []Episode{{
			Request:  Request{Method: "GET", URL: "/ping"},
			Response: Response{Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: json.RawMessage(`{"ok":true}`)},
		}},
	}
	if err := SaveCassette(path, cass); err != nil {
		t.Fatal(err)
	}

	r, err := NewRecorder(Config{Mode: ModeReplay, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// Note: URL must match cassette (relative). Build via http.Request directly.
	req, _ := http.NewRequest("GET", "http://x/ping", nil)
	// Strip for match: replay compares recorded URL strings.
	req.URL.Scheme = ""
	req.URL.Host = ""
	resp, err := r.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(b, []byte("ok")) {
		t.Errorf("body=%q", b)
	}
}

func TestAutoPicksRecordWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto.json")
	r, err := NewRecorder(Config{Mode: ModeAuto, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != ModeRecord {
		t.Errorf("mode=%v want record", r.Mode())
	}
}

func TestAutoPicksReplayWhenPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto2.json")
	_ = SaveCassette(path, &Cassette{Version: 1, Episodes: []Episode{{Request: Request{Method: "GET", URL: "/x"}, Response: Response{Status: 200}}}})
	r, err := NewRecorder(Config{Mode: ModeAuto, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode() != ModeReplay {
		t.Errorf("mode=%v want replay", r.Mode())
	}
}

func TestScannerFailsOnBearerLeak(t *testing.T) {
	ep := Episode{
		Request:  Request{Method: "GET", URL: "/x"},
		Response: Response{Status: 200, Body: json.RawMessage(`"Bearer ABCDEFGHIJKLMNOPQRSTUVWXYZ"`)},
	}
	c := &Cassette{Version: 1, Episodes: []Episode{ep}}
	data, _ := marshalIndent(c)
	if err := Scan(data, RedactOptions{}); err == nil {
		t.Fatal("expected scanner to fail")
	}
}

func TestScannerKnownSecret(t *testing.T) {
	c := &Cassette{Version: 1, Episodes: []Episode{{
		Request:  Request{Method: "GET", URL: "/x"},
		Response: Response{Status: 200, Body: json.RawMessage(`"mytoken123"`)},
	}}}
	data, _ := marshalIndent(c)
	if err := Scan(data, RedactOptions{KnownSecrets: []string{"mytoken123"}}); err == nil {
		t.Fatal("expected known-secret failure")
	}
}

func TestSequenceReturnsNthEpisode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seq.json")
	_ = SaveCassette(path, &Cassette{Version: 1, Episodes: []Episode{
		{Request: Request{Method: "GET", URL: "/x"}, Response: Response{Status: 200, Body: json.RawMessage(`"one"`)}},
		{Request: Request{Method: "GET", URL: "/x"}, Response: Response{Status: 200, Body: json.RawMessage(`"two"`)}},
	}})
	r, _ := NewRecorder(Config{Mode: ModeReplay, Path: path})
	req, _ := http.NewRequest("GET", "/x", nil)
	r1, _ := r.RoundTrip(req)
	b1, _ := io.ReadAll(r1.Body)
	req2, _ := http.NewRequest("GET", "/x", nil)
	r2, _ := r.RoundTrip(req2)
	b2, _ := io.ReadAll(r2.Body)
	if string(b1) == string(b2) {
		t.Errorf("expected distinct sequenced responses, got %q and %q", b1, b2)
	}
}

func TestMatchIgnoreJSONPath(t *testing.T) {
	a := Request{Method: "POST", URL: "/x", Body: json.RawMessage(`{"id":1,"name":"x"}`)}
	b := Request{Method: "POST", URL: "/x", Body: json.RawMessage(`{"id":2,"name":"x"}`)}
	if Match(a, b, MatchOptions{}) {
		t.Fatal("should not match without ignore")
	}
	if !Match(a, b, MatchOptions{IgnoreJSONPaths: []string{"$.id"}}) {
		t.Fatal("should match when $.id ignored")
	}
}

func TestReplayMissErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "miss.json")
	_ = SaveCassette(path, &Cassette{Version: 1})
	r, _ := NewRecorder(Config{Mode: ModeReplay, Path: path})
	req, _ := http.NewRequest("GET", "/nope", nil)
	if _, err := r.RoundTrip(req); err == nil {
		t.Fatal("expected miss error")
	}
}
