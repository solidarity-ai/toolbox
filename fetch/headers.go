package fetch

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

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

// NewHeadersFromPairs creates Headers from a sequence of [name, value] pairs.
func NewHeadersFromPairs(pairs [][2]string) (*Headers, error) {
	h := &Headers{}
	for _, pair := range pairs {
		if err := validateHeaderName(pair[0]); err != nil {
			return nil, err
		}
		value := normalizeHeaderValue(pair[1])
		if err := validateHeaderValue(value); err != nil {
			return nil, err
		}
		h.list = append(h.list, [2]string{strings.ToLower(pair[0]), value})
	}
	h.sort()
	return h, nil
}

// NewHeadersFromRecord creates Headers from a map of name -> value.
func NewHeadersFromRecord(record map[string]string) (*Headers, error) {
	h := &Headers{}
	for name, value := range record {
		if err := validateHeaderName(name); err != nil {
			return nil, err
		}
		if err := validateHeaderValue(value); err != nil {
			return nil, err
		}
		h.list = append(h.list, [2]string{strings.ToLower(name), value})
	}
	h.sort()
	return h, nil
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

// Delete removes all values for the given header name.
func (h *Headers) Delete(name string) error {
	if err := validateHeaderName(name); err != nil {
		return err
	}
	lower := strings.ToLower(name)
	filtered := h.list[:0]
	for _, entry := range h.list {
		if entry[0] != lower {
			filtered = append(filtered, entry)
		}
	}
	h.list = filtered
	return nil
}

// Get returns the combined value for a header name, or "" with ok=false if not present.
func (h *Headers) Get(name string) (string, bool, error) {
	if err := validateHeaderName(name); err != nil {
		return "", false, err
	}
	lower := strings.ToLower(name)
	var values []string
	for _, entry := range h.list {
		if entry[0] == lower {
			values = append(values, entry[1])
		}
	}
	if len(values) == 0 {
		return "", false, nil
	}
	return strings.Join(values, ", "), true, nil
}

// Has returns whether the given header name exists.
func (h *Headers) Has(name string) (bool, error) {
	if err := validateHeaderName(name); err != nil {
		return false, err
	}
	lower := strings.ToLower(name)
	for _, entry := range h.list {
		if entry[0] == lower {
			return true, nil
		}
	}
	return false, nil
}

// Set replaces all values for the given header name with a single value.
// Per the Fetch spec, value is normalized (trimmed) before validation.
func (h *Headers) Set(name, value string) error {
	if err := validateHeaderName(name); err != nil {
		return err
	}
	value = normalizeHeaderValue(value)
	if err := validateHeaderValue(value); err != nil {
		return err
	}
	lower := strings.ToLower(name)

	// Remove all existing entries with this name.
	filtered := h.list[:0]
	for _, entry := range h.list {
		if entry[0] != lower {
			filtered = append(filtered, entry)
		}
	}
	h.list = append(filtered, [2]string{lower, value})
	h.sort()
	return nil
}

// GetSetCookie returns individual Set-Cookie header values without combining.
func (h *Headers) GetSetCookie() []string {
	var result []string
	for _, entry := range h.list {
		if entry[0] == "set-cookie" {
			result = append(result, entry[1])
		}
	}
	return result
}

// Entries returns all combined header entries sorted by name.
// Set-Cookie headers are not combined per the Fetch spec.
func (h *Headers) Entries() [][2]string {
	return h.combined()
}

// Keys returns sorted, deduplicated header names.
func (h *Headers) Keys() []string {
	entries := h.combined()
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e[0]
	}
	return keys
}

// Values returns header values in sorted name order, combined per name.
func (h *Headers) Values() []string {
	entries := h.combined()
	vals := make([]string, len(entries))
	for i, e := range entries {
		vals[i] = e[1]
	}
	return vals
}

// ForEach calls fn for each unique header name with its combined value.
func (h *Headers) ForEach(fn func(value, name string)) {
	for _, entry := range h.combined() {
		fn(entry[1], entry[0])
	}
}

// Len returns the number of unique header names.
func (h *Headers) Len() int {
	return len(h.combined())
}

// ToHTTPHeader converts to Go's net/http.Header format.
func (h *Headers) ToHTTPHeader() http.Header {
	result := make(http.Header)
	for _, entry := range h.list {
		result.Add(entry[0], entry[1])
	}
	return result
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

// RawList returns the underlying sorted list of [name, value] pairs.
// This is useful for iteration where individual entries matter (not combined).
func (h *Headers) RawList() [][2]string {
	return h.list
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
