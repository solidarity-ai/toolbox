// Package vcr implements record/replay of HTTP interactions for tooltest.
// It is unexported; the public surface lives in testutil/tooltest/vcr.go.
package vcr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CassetteVersion is the on-disk format version.
const CassetteVersion = 1

// Cassette is the serialized form of recorded HTTP interactions.
type Cassette struct {
	Version  int       `json:"version"`
	Episodes []Episode `json:"episodes"`
}

// Episode is a single request/response pair.
type Episode struct {
	Request    Request   `json:"request"`
	Response   Response  `json:"response"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Request is the recorded request side.
type Request struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers,omitempty"`
	// Body is raw bytes when not JSON, or parsed JSON otherwise.
	Body json.RawMessage `json:"body,omitempty"`
}

// Response is the recorded response side.
type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    json.RawMessage     `json:"body,omitempty"`
}

// LoadCassette reads a cassette from disk. Returns an empty cassette if
// the file does not exist.
func LoadCassette(path string) (*Cassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Cassette{Version: CassetteVersion}, nil
		}
		return nil, err
	}
	var c Cassette
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse cassette %s: %w", path, err)
	}
	if c.Version == 0 {
		c.Version = CassetteVersion
	}
	return &c, nil
}

// SaveCassette writes a cassette to disk, pretty-printed with stable keys.
func SaveCassette(path string, c *Cassette) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	c.Version = CassetteVersion
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// HeadersToMap canonicalizes an http.Header with sorted values.
func HeadersToMap(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vs := make([]string, len(h[k]))
		copy(vs, h[k])
		out[k] = vs
	}
	return out
}

// MapToHeaders reverses HeadersToMap.
func MapToHeaders(m map[string][]string) http.Header {
	h := make(http.Header, len(m))
	for k, vs := range m {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return h
}

// EncodeBody stores a raw byte body as parsed JSON when the content-type is
// JSON and the bytes parse, otherwise as a JSON-encoded string.
func EncodeBody(contentType string, body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	if isJSONContentType(contentType) {
		var probe any
		if err := json.Unmarshal(body, &probe); err == nil {
			// Re-marshal to canonicalize.
			out, err := json.Marshal(probe)
			if err == nil {
				return out
			}
		}
	}
	out, _ := json.Marshal(string(body))
	return out
}

// DecodeBody turns a stored body back into bytes.
func DecodeBody(contentType string, raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	if isJSONContentType(contentType) {
		var probe any
		if err := json.Unmarshal(raw, &probe); err == nil {
			if s, ok := probe.(string); ok {
				return []byte(s)
			}
			out, err := json.Marshal(probe)
			if err == nil {
				return out
			}
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	return []byte(raw)
}

func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	for i := 0; i < len(ct); i++ {
		if ct[i] == ';' {
			ct = ct[:i]
			break
		}
	}
	switch ct {
	case "application/json", "text/json":
		return true
	}
	// application/vnd.api+json etc.
	if len(ct) > 5 && ct[len(ct)-5:] == "+json" {
		return true
	}
	return false
}
