// Package tooltest — VCR support.
//
// VCR is a record-once-replay-forever http.RoundTripper. It slots into
// toolset.Config.FetchTransport exactly like FetchMock. Typical use:
//
//	vcr := tooltest.NewVCR(t, "github_issues")
//	prepared := tooltest.PrepareToolset(t, decl, toolset.Config{
//	    FetchTransport: vcr,
//	})
//
// On first run with VCR_MODE=record, the recorder proxies to the real API,
// redacts credentials, and writes testdata/vcr/<name>.json next to the test.
// On subsequent runs (default mode: replay) responses are served from the
// cassette — no network, deterministic, CI-safe.
//
// For record mode against a real API, wire the credential into credentialrepo
// via `toolbox auth` first. The redactor scrubs Authorization/Cookie before
// save and the non-disableable secret scanner refuses to write cassettes that
// leak a bearer token, a JWT, or any caller-provided KnownSecrets.
//
// VCR can also be used as a hand-written mocker via Handle/JSON/Status.
// Compose real recordings with injected errors using FallbackTo:
//
//	vcr := tooltest.NewVCR(t, "api_happy_path").
//	    FallbackTo(tooltest.NewFetchMock().Status("api.example.com/fail", 500))
package tooltest

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/testutil/tooltest/internal/vcr"
)

// VCR is an http.RoundTripper that records and replays HTTP interactions.
type VCR struct {
	rec   *vcr.Recorder
	t     testing.TB
	mu    sync.Mutex
	calls []RecordedCall
}

// VCROption configures a VCR.
type VCROption func(*vcrConfig)

type vcrConfig struct {
	mode         vcr.Mode
	match        vcr.MatchOptions
	redact       vcr.RedactOptions
	real         http.RoundTripper
	stripHost    string
	pathOverride string
}

// WithMode overrides the mode (default: VCR_MODE env, else replay).
func WithMode(m string) VCROption {
	return func(c *vcrConfig) {
		parsed, err := vcr.ParseMode(m, vcr.ModeReplay)
		if err == nil {
			c.mode = parsed
		}
	}
}

// WithIgnoreHeaders configures header names ignored during matching.
func WithIgnoreHeaders(names ...string) VCROption {
	return func(c *vcrConfig) { c.match.IgnoreHeaders = append(c.match.IgnoreHeaders, names...) }
}

// WithIgnoreJSONPaths configures JSON body paths ignored during matching.
func WithIgnoreJSONPaths(paths ...string) VCROption {
	return func(c *vcrConfig) { c.match.IgnoreJSONPaths = append(c.match.IgnoreJSONPaths, paths...) }
}

// WithKnownSecrets registers literal secrets the scanner must not find.
func WithKnownSecrets(secrets ...string) VCROption {
	return func(c *vcrConfig) { c.redact.KnownSecrets = append(c.redact.KnownSecrets, secrets...) }
}

// WithAllowedPatterns registers regexes exempt from the secret scanner.
func WithAllowedPatterns(patterns ...string) VCROption {
	return func(c *vcrConfig) { c.redact.Allowed = append(c.redact.Allowed, patterns...) }
}

// WithRedactHeaders adds header names to the redaction pipeline.
func WithRedactHeaders(names ...string) VCROption {
	return func(c *vcrConfig) { c.redact.Headers = append(c.redact.Headers, names...) }
}

// WithRedactJSONPaths adds JSON body paths to the redaction pipeline.
func WithRedactJSONPaths(paths ...string) VCROption {
	return func(c *vcrConfig) { c.redact.JSONPaths = append(c.redact.JSONPaths, paths...) }
}

// WithRealTransport sets the transport used in record mode (default: DefaultTransport).
func WithRealTransport(rt http.RoundTripper) VCROption {
	return func(c *vcrConfig) { c.real = rt }
}

// WithStripHost strips the given host (with scheme) from recorded URLs so
// cassettes made against httptest.NewServer are portable.
func WithStripHost(host string) VCROption {
	return func(c *vcrConfig) { c.stripHost = host }
}

// WithCassettePath overrides the default testdata/vcr/<name>.json location.
func WithCassettePath(path string) VCROption {
	return func(c *vcrConfig) { c.pathOverride = path }
}

// NewVCR constructs a VCR for a test. The cassette lives at
// testdata/vcr/<cassetteName>.json relative to the caller's test file.
func NewVCR(t testing.TB, cassetteName string, opts ...VCROption) *VCR {
	t.Helper()

	envMode, err := vcr.EnvMode(vcr.ModeReplay)
	if err != nil {
		t.Fatalf("vcr: %v", err)
	}
	cfg := vcrConfig{mode: envMode}
	for _, o := range opts {
		o(&cfg)
	}

	path := cfg.pathOverride
	if path == "" {
		path = defaultCassettePath(cassetteName)
	}

	rec, err := vcr.NewRecorder(vcr.Config{
		Mode:          cfg.mode,
		Path:          path,
		Match:         cfg.match,
		Redact:        cfg.redact,
		RealTransport: cfg.real,
		StripHost:     cfg.stripHost,
	})
	if err != nil {
		t.Fatalf("vcr: new recorder: %v", err)
	}

	v := &VCR{
		rec: rec,
		t:   t,
	}
	t.Cleanup(func() {
		if rec.Mode() == vcr.ModeRecord {
			if err := rec.Save(); err != nil {
				t.Errorf("vcr: save cassette: %v", err)
			}
		}
	})
	return v
}

// Mode returns the active mode as a string ("record" or "replay").
func (v *VCR) Mode() string { return v.rec.Mode().String() }

// CassettePath returns the on-disk cassette path.
func (v *VCR) CassettePath() string { return v.rec.Path() }

// Calls returns recorded calls observed by RoundTrip. Reuses the existing
// RecordedCall type from fetch_mock.go.
func (v *VCR) Calls() []RecordedCall {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]RecordedCall, len(v.calls))
	copy(out, v.calls)
	return out
}

// FallbackTo installs a fallback transport consulted on replay miss.
func (v *VCR) FallbackTo(rt http.RoundTripper) *VCR {
	v.rec.SetFallback(rt)
	return v
}

// Handle injects a synthetic episode, making VCR usable as an ergonomic mock.
// method+pattern defines the match key. The handler is invoked immediately
// against an httptest recorder to capture its response.
func (v *VCR) Handle(method, url string, handler http.HandlerFunc) *VCR {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, nil)
	handler(rr, req)
	resp := rr.Result()
	defer resp.Body.Close()

	body := rr.Body.Bytes()
	ep := vcr.Episode{
		Request: vcr.Request{
			Method: method,
			URL:    url,
		},
		Response: vcr.Response{
			Status:  resp.StatusCode,
			Headers: vcr.HeadersToMap(resp.Header),
			Body:    vcr.EncodeBody(resp.Header.Get("Content-Type"), body),
		},
		RecordedAt: time.Now().UTC(),
	}
	v.rec.AddSynthetic(ep)
	return v
}

// JSON returns a handler writing a JSON body with the given status.
func JSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// Status returns a handler writing an empty body with the given status.
func Status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// RoundTrip implements http.RoundTripper.
func (v *VCR) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := v.rec.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	v.recordCall(req)
	return resp, nil
}

func (v *VCR) recordCall(req *http.Request) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, RecordedCall{
		Method:  req.Method,
		URL:     req.URL.String(),
		Host:    req.URL.Host,
		Path:    req.URL.Path,
		Headers: req.Header.Clone(),
	})
}

// defaultCassettePath returns testdata/vcr/<name>.json next to the caller's
// test file. Walks the stack to skip frames inside this package.
func defaultCassettePath(name string) string {
	_, self, _, _ := runtime.Caller(0)
	selfDir := filepath.Dir(self)
	for skip := 1; skip < 32; skip++ {
		_, file, _, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		if filepath.Dir(file) == selfDir {
			continue
		}
		return filepath.Join(filepath.Dir(file), "testdata", "vcr", name+".json")
	}
	return filepath.Join("testdata", "vcr", name+".json")
}
