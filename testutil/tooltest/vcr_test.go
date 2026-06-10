package tooltest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestVCR_SaveAndReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Authorization", "Bearer server-secret-abcdefghijklmnop")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	dir := t.TempDir()
	path := filepath.Join(dir, "saved.json")

	// Record using a nested test so t.Cleanup saves when it returns.
	t.Run("record", func(t *testing.T) {
		rec := tooltest.NewVCR(t, "saved",
			tooltest.WithCassettePath(path),
			tooltest.WithMode("record"),
			tooltest.WithStripHost(host),
		)
		client := &http.Client{Transport: rec}
		req, _ := http.NewRequest("GET", srv.URL+"/ping", nil)
		req.Header.Set("Authorization", "Bearer client-secret-abcdefghijklmnop")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})

	// Cassette should now exist and contain no bearer.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	if strings.Contains(string(data), "server-secret-abcdefghijklmnop") {
		t.Errorf("cassette leaked server bearer")
	}
	if strings.Contains(string(data), "client-secret-abcdefghijklmnop") {
		t.Errorf("cassette leaked client bearer")
	}

	// Replay: no server needed. Use the stripped-URL form recorded in cassette.
	rep := tooltest.NewVCR(t, "saved",
		tooltest.WithCassettePath(path),
		tooltest.WithMode("replay"),
	)
	_ = rep
	// Request path only — matches the stripped URL.
	req, _ := http.NewRequest("GET", "http://placeholder/ping", nil)
	// Rewrite to match relative URL used by recorder: we replay by direct
	// RoundTrip with a URL lacking scheme/host.
	req.URL.Scheme = ""
	req.URL.Host = ""
	resp, err := rep.RoundTrip(req)
	if err != nil {
		t.Fatalf("replay roundtrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "ok") {
		t.Errorf("replay body=%q", body)
	}
}

func TestVCR_HandleSynthetic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "synth.json")
	v := tooltest.NewVCR(t, "synth",
		tooltest.WithCassettePath(path),
		tooltest.WithMode("replay"),
	)
	v.Handle("GET", "/foo", tooltest.JSON(200, `{"x":1}`))

	req, _ := http.NewRequest("GET", "http://placeholder/foo", nil)
	req.URL.Scheme = ""
	req.URL.Host = ""
	resp, err := v.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "x") {
		t.Errorf("body=%q", body)
	}
}

func TestVCR_FallbackToOnMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.json")
	v := tooltest.NewVCR(t, "fb",
		tooltest.WithCassettePath(path),
		tooltest.WithMode("replay"),
	)
	fallback := tooltest.NewFetchMock().JSON("example.com/x", `{"via":"fallback"}`)
	v.FallbackTo(fallback)

	client := &http.Client{Transport: v}
	resp, err := client.Get("https://example.com/x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "fallback") {
		t.Errorf("body=%q", body)
	}
}

// TestVCR_InvokeRun_RecordAndReplay wires VCR into toolset.Config and runs a
// real tool via invoke.Run. The cassette is committed under testdata/vcr/
// and subsequent runs replay without touching the network.
func TestVCR_InvokeRun_RecordAndReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pong":true}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cassette := filepath.Join("testdata", "vcr", "invoke_ping.json")
	mode := "replay"
	if _, err := os.Stat(cassette); os.IsNotExist(err) {
		mode = "record"
	}

	opts := []tooltest.VCROption{
		tooltest.WithCassettePath(cassette),
		tooltest.WithMode(mode),
	}
	if mode == "record" {
		opts = append(opts, tooltest.WithStripHost(host))
	}
	vcr := tooltest.NewVCR(t, "invoke_ping", opts...)

	// In replay mode the fetch-test tool will call the URL baked into the
	// cassette (stripped of host). The toolbox fetch transport needs an
	// allowlist entry; allow all for this fixture.
	prepared := tooltest.PrepareToolset(t,
		tooltest.DistPackageDecl("fetch-test"),
		toolset.Config{
			FetchTransport: vcr,
			CredentialPolicySource: credentialrepo.StaticPolicySource{
				tooldef.ModulePath("fixtures.local/fetch-test"): {
					Allowlist: transport.NewHostAllowlist([]string{"*"}),
				},
			},
		})

	url := srv.URL + "/ping"
	if mode == "replay" {
		// Cassette was recorded with stripHost, so the URL stored is "/ping".
		// Use any routable URL whose path matches; the recorder strips host
		// during matching only if StripHost is set, so in replay we must
		// invoke with a URL that matches the recorded form. Easiest: use a
		// placeholder host and rely on match against the live URL. But the
		// live URL will include scheme+host. So in replay, we re-enable
		// StripHost with the placeholder host the cassette used.
		// Simpler: rerun recording against a fixed-port we can't control.
		//
		// Practical solution: in replay, supply a dummy transport host via
		// the same live server (still up in this test), so URLs still
		// strip to the same form using WithStripHost.
		vcr2 := tooltest.NewVCR(t, "invoke_ping",
			tooltest.WithCassettePath(cassette),
			tooltest.WithMode("replay"),
			tooltest.WithStripHost(host),
		)
		prepared = tooltest.PrepareToolset(t,
			tooltest.DistPackageDecl("fetch-test"),
			toolset.Config{
				FetchTransport: vcr2,
				CredentialPolicySource: credentialrepo.StaticPolicySource{
					tooldef.ModulePath("fixtures.local/fetch-test"): {
						Allowlist: transport.NewHostAllowlist([]string{"*"}),
					},
				},
			})
	}

	result, err := tooltest.RunInvokeString(t, invoke.Run, prepared, "fetchTest.get", map[string]any{"url": url})
	if err != nil {
		t.Fatalf("invoke.Run: %v", err)
	}
	if !strings.Contains(result, "pong") {
		t.Errorf("result=%q, want pong", result)
	}
}

func TestVCR_StatusHelper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	v := tooltest.NewVCR(t, "status",
		tooltest.WithCassettePath(path),
		tooltest.WithMode("replay"),
	)
	v.Handle("GET", "/fail", tooltest.Status(503))
	req, _ := http.NewRequest("GET", "/fail", nil)
	resp, err := v.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("status=%d", resp.StatusCode)
	}
}
