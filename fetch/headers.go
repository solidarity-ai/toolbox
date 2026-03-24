package fetch

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// NOTE: Headers methods beyond NewHeaders, Append, Entries, Clone, and
// NewHeadersFromHTTP are intentionally minimal. The full Fetch API surface
// (get, set, delete, has, forEach, iteration) lives in pure JS — see
// runtime/quickts/fetch.go. The Go side only needs enough to build request
// headers and parse response headers for the fetch() bridge.

// Headers implements the Fetch API Headers interface.
// https://fetch.spec.whatwg.org/#headers-class
//
// Header names are case-insensitive and stored in lowercase.
// Multiple values for the same header are combined with ", ".
type Headers struct {
	// list stores headers as an ordered list of name/value pairs.
	// Names are lowercased. The list is kept sorted by name.
	list [][2]string
}

// NewHeaders creates a new empty Headers.
func NewHeaders() *Headers {
	return &Headers{}
}

// Clone creates a deep copy of the Headers.
func (h *Headers) Clone() *Headers {
	clone := &Headers{
		list: make([][2]string, len(h.list)),
	}
	copy(clone.list, h.list)
	return clone
}

// Append adds a new value for the given header name.
// Per the Fetch spec, value is normalized (trimmed) before validation.
func (h *Headers) Append(name, value string) error {
	if err := validateHeaderName(name); err != nil {
		return err
	}
	value = normalizeHeaderValue(value)
	if err := validateHeaderValue(value); err != nil {
		return err
	}
	h.list = append(h.list, [2]string{strings.ToLower(name), value})
	h.sort()
	return nil
}

// Entries returns all combined header entries sorted by name.
// Set-Cookie headers are not combined per the Fetch spec.
func (h *Headers) Entries() [][2]string {
	return h.combined()
}

// NewHeadersFromHTTP creates Headers from a net/http.Header.
func NewHeadersFromHTTP(hh http.Header) *Headers {
	h := &Headers{}
	for name, values := range hh {
		for _, v := range values {
			h.list = append(h.list, [2]string{strings.ToLower(name), v})
		}
	}
	h.sort()
	return h
}

// combined returns entries with values combined per unique name.
// Set-Cookie headers are never combined — each appears as a separate entry
// per the Fetch spec.
func (h *Headers) combined() [][2]string {
	if len(h.list) == 0 {
		return [][2]string{}
	}
	var result [][2]string
	var prevName string
	for _, entry := range h.list {
		if entry[0] == "set-cookie" {
			// Set-Cookie is never combined.
			result = append(result, [2]string{entry[0], entry[1]})
		} else if entry[0] == prevName && len(result) > 0 {
			result[len(result)-1][1] += ", " + entry[1]
		} else {
			result = append(result, [2]string{entry[0], entry[1]})
		}
		prevName = entry[0]
	}
	return result
}

func (h *Headers) sort() {
	sort.SliceStable(h.list, func(i, j int) bool {
		return h.list[i][0] < h.list[j][0]
	})
}

// validateHeaderName checks if a header name is valid per the Fetch spec.
// A valid header name matches the HTTP token production.
func validateHeaderName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: header name must not be empty", ErrInvalidHeaderName)
	}
	for _, c := range name {
		if !isTokenChar(byte(c)) || c > 127 {
			return fmt.Errorf("%w: %q", ErrInvalidHeaderName, name)
		}
	}
	return nil
}

// validateHeaderValue checks if a header value is valid per the Fetch spec.
// Values must not contain NUL (0x00), CR (0x0D), or LF (0x0A).
// Code points > 0xFF are rejected (not representable as bytes).
func validateHeaderValue(value string) error {
	for _, c := range value {
		if c == 0x00 || c == 0x0A || c == 0x0D {
			return fmt.Errorf("%w: %q", ErrInvalidHeaderValue, value)
		}
		if c > 0xFF {
			return fmt.Errorf("%w: %q", ErrInvalidHeaderValue, value)
		}
	}
	return nil
}

// normalizeHeaderValue strips leading and trailing HTTP whitespace
// (0x09 tab, 0x0A LF, 0x0D CR, 0x20 space) per the Fetch spec.
func normalizeHeaderValue(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == 0x09 || r == 0x0A || r == 0x0D || r == 0x20
	})
}

// isTokenChar returns true for characters valid in HTTP tokens (RFC 7230).
func isTokenChar(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' ||
		c == '\'' || c == '*' || c == '+' || c == '-' || c == '.' ||
		c == '^' || c == '_' || c == '`' || c == '|' || c == '~':
		return true
	default:
		return false
	}
}
