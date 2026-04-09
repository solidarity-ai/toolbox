package vcr

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Mode selects record/replay behavior.
type Mode int

const (
	// ModeReplay reads episodes from the cassette; unmatched requests error.
	ModeReplay Mode = iota
	// ModeRecord forwards requests to a real transport and records them.
	ModeRecord
	// ModeAuto records if the cassette is missing, otherwise replays.
	ModeAuto
)

func (m Mode) String() string {
	switch m {
	case ModeRecord:
		return "record"
	case ModeReplay:
		return "replay"
	case ModeAuto:
		return "auto"
	}
	return "unknown"
}

// ParseMode parses a mode string, returning def when s is empty.
func ParseMode(s string, def Mode) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def, nil
	case "record":
		return ModeRecord, nil
	case "replay":
		return ModeReplay, nil
	case "auto":
		return ModeAuto, nil
	}
	return def, fmt.Errorf("vcr: invalid mode %q", s)
}

// EnvMode returns the mode from VCR_MODE, or def if unset.
func EnvMode(def Mode) (Mode, error) {
	return ParseMode(os.Getenv("VCR_MODE"), def)
}

// Recorder implements http.RoundTripper with record/replay.
type Recorder struct {
	mu            sync.Mutex
	mode          Mode
	path          string
	cassette      *Cassette
	match         MatchOptions
	redact        RedactOptions
	realTransport http.RoundTripper
	fallback      http.RoundTripper
	stripHost     string // strip this host prefix from recorded URLs (for httptest portability)
	seq           *sequenceTracker
	dirty         bool
}

// Config configures a Recorder.
type Config struct {
	Mode          Mode
	Path          string
	Match         MatchOptions
	Redact        RedactOptions
	RealTransport http.RoundTripper
	Fallback      http.RoundTripper
	StripHost     string
}

// NewRecorder builds a Recorder and loads any existing cassette at cfg.Path.
func NewRecorder(cfg Config) (*Recorder, error) {
	cass, err := LoadCassette(cfg.Path)
	if err != nil {
		return nil, err
	}
	mode := cfg.Mode
	if mode == ModeAuto {
		if len(cass.Episodes) == 0 {
			mode = ModeRecord
		} else {
			mode = ModeReplay
		}
	}
	r := &Recorder{
		mode:          mode,
		path:          cfg.Path,
		cassette:      cass,
		match:         cfg.Match,
		redact:        cfg.Redact,
		realTransport: cfg.RealTransport,
		fallback:      cfg.Fallback,
		stripHost:     cfg.StripHost,
		seq:           newSequenceTracker(),
	}
	if r.realTransport == nil {
		r.realTransport = http.DefaultTransport
	}
	return r, nil
}

// Mode returns the active mode.
func (r *Recorder) Mode() Mode { return r.mode }

// Path returns the cassette path.
func (r *Recorder) Path() string { return r.path }

// Episodes returns a copy of the cassette episodes.
func (r *Recorder) Episodes() []Episode {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Episode, len(r.cassette.Episodes))
	copy(out, r.cassette.Episodes)
	return out
}

// SetFallback installs a fallback transport consulted on replay miss.
func (r *Recorder) SetFallback(rt http.RoundTripper) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = rt
}

// AddSynthetic appends a pre-built episode (used by Handle).
func (r *Recorder) AddSynthetic(ep Episode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cassette.Episodes = append(r.cassette.Episodes, ep)
}

// RoundTrip implements http.RoundTripper.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBody, err := drainBody(req)
	if err != nil {
		return nil, err
	}
	recorded := r.toRecordedRequest(req, reqBody)

	switch r.mode {
	case ModeReplay:
		if resp, ok := r.replay(recorded, req); ok {
			return resp, nil
		}
		if r.fallback != nil {
			// Restore body for fallback.
			req.Body = io.NopCloser(bytes.NewReader(reqBody))
			return r.fallback.RoundTrip(req)
		}
		return nil, fmt.Errorf("vcr: no matching episode for %s %s (cassette %s)", recorded.Method, recorded.URL, r.path)
	case ModeRecord:
		// Restore body for the real transport.
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		resp, err := r.realTransport.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		return r.record(recorded, resp)
	}
	return nil, fmt.Errorf("vcr: unsupported mode %v", r.mode)
}

func (r *Recorder) replay(live Request, orig *http.Request) (*http.Response, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := live.Method + " " + live.URL
	offset := r.seq.next(key)
	seen := 0
	for _, ep := range r.cassette.Episodes {
		if Match(live, ep.Request, r.match) {
			if seen == offset {
				return buildResponse(ep.Response, orig), true
			}
			seen++
		}
	}
	// Fall back to the first match if the sequence index is out of range.
	for _, ep := range r.cassette.Episodes {
		if Match(live, ep.Request, r.match) {
			return buildResponse(ep.Response, orig), true
		}
	}
	return nil, false
}

func (r *Recorder) record(recReq Request, resp *http.Response) (*http.Response, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	ep := Episode{
		Request: recReq,
		Response: Response{
			Status:  resp.StatusCode,
			Headers: HeadersToMap(resp.Header),
			Body:    EncodeBody(resp.Header.Get("Content-Type"), respBody),
		},
		RecordedAt: time.Now().UTC(),
	}
	Redact(&ep, r.redact)

	r.mu.Lock()
	r.cassette.Episodes = append(r.cassette.Episodes, ep)
	r.dirty = true
	r.mu.Unlock()
	return resp, nil
}

// Save serializes the cassette to disk and runs the non-disableable scanner.
// Safe to call multiple times.
func (r *Recorder) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty {
		return nil
	}
	// Scan first by serializing in memory.
	tmp := &Cassette{Version: CassetteVersion, Episodes: r.cassette.Episodes}
	data, err := marshalIndent(tmp)
	if err != nil {
		return err
	}
	if err := Scan(data, r.redact); err != nil {
		return err
	}
	if err := SaveCassette(r.path, tmp); err != nil {
		return err
	}
	r.dirty = false
	return nil
}

func (r *Recorder) toRecordedRequest(req *http.Request, body []byte) Request {
	url := req.URL.String()
	if r.stripHost != "" && strings.Contains(url, r.stripHost) {
		url = strings.Replace(url, "http://"+r.stripHost, "", 1)
		url = strings.Replace(url, "https://"+r.stripHost, "", 1)
	}
	return Request{
		Method:  req.Method,
		URL:     url,
		Headers: HeadersToMap(req.Header),
		Body:    EncodeBody(req.Header.Get("Content-Type"), body),
	}
}

func buildResponse(rr Response, orig *http.Request) *http.Response {
	body := DecodeBody(headerLookup(rr.Headers, "Content-Type"), rr.Body)
	h := MapToHeaders(rr.Headers)
	if h == nil {
		h = make(http.Header)
	}
	return &http.Response{
		StatusCode:    rr.Status,
		Status:        fmt.Sprintf("%d %s", rr.Status, http.StatusText(rr.Status)),
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       orig,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}

func headerLookup(m map[string][]string, name string) string {
	for k, v := range m {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func drainBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

func marshalIndent(c *Cassette) ([]byte, error) {
	var buf bytes.Buffer
	enc := jsonEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
