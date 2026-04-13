package vcr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RedactOptions configures the redaction pipeline run before save.
type RedactOptions struct {
	Headers      []string // header names to replace with "REDACTED"
	JSONPaths    []string // JSON paths in request/response bodies to replace with "REDACTED"
	BodyPatterns []string // regex patterns to replace in body text with "REDACTED"
	KnownSecrets []string // literal secrets that must NOT appear in saved cassette
	Allowed      []string // regex patterns excluded from scanner matches
}

// Built-in always-on header redactors.
var builtinRedactHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"Proxy-Authorization",
}

// Non-disableable secret scanner patterns.
var builtinScannerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9_\-\.]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.`),
}

// Redact mutates the episode in place, scrubbing configured and built-in secrets.
func Redact(ep *Episode, opts RedactOptions) {
	headers := append([]string{}, builtinRedactHeaders...)
	headers = append(headers, opts.Headers...)

	ep.Request.Headers = redactHeaders(ep.Request.Headers, headers)
	ep.Response.Headers = redactHeaders(ep.Response.Headers, headers)

	ep.Request.Body = redactJSONBody(ep.Request.Body, opts.JSONPaths)
	ep.Response.Body = redactJSONBody(ep.Response.Body, opts.JSONPaths)

	for _, pat := range opts.BodyPatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		ep.Request.Body = redactPatternInBody(ep.Request.Body, re)
		ep.Response.Body = redactPatternInBody(ep.Response.Body, re)
	}
}

func redactHeaders(h map[string][]string, names []string) map[string][]string {
	if len(h) == 0 {
		return h
	}
	lower := make(map[string]bool, len(names))
	for _, n := range names {
		lower[strings.ToLower(n)] = true
	}
	for k := range h {
		if lower[strings.ToLower(k)] {
			h[k] = []string{"REDACTED"}
		}
	}
	return h
}

func redactJSONBody(raw json.RawMessage, paths []string) json.RawMessage {
	if len(raw) == 0 || len(paths) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	for _, p := range paths {
		setJSONPath(v, p, "REDACTED")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func redactPatternInBody(raw json.RawMessage, re *regexp.Regexp) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	v = walkReplace(v, re)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func walkReplace(v any, re *regexp.Regexp) any {
	switch n := v.(type) {
	case string:
		return re.ReplaceAllString(n, "REDACTED")
	case map[string]any:
		for k, vv := range n {
			n[k] = walkReplace(vv, re)
		}
		return n
	case []any:
		for i, el := range n {
			n[i] = walkReplace(el, re)
		}
		return n
	}
	return v
}

// setJSONPath sets values at path to repl. Reuses walker from matcher.
func setJSONPath(root any, path, repl string) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "$") {
		return
	}
	rest := strings.TrimPrefix(path, "$")
	walkSet(root, rest, repl)
}

func walkSet(node any, rest, repl string) {
	if rest == "" {
		return
	}
	if strings.HasPrefix(rest, "..") {
		key, tail := splitNext(rest[2:])
		recursiveSet(node, key, tail, repl)
		return
	}
	if strings.HasPrefix(rest, ".") {
		rest = rest[1:]
	}
	key, tail := splitNext(rest)
	if key == "" {
		return
	}
	if idx := strings.Index(key, "["); idx >= 0 {
		name := key[:idx]
		bracket := key[idx:]
		child := node
		if name != "" {
			m, ok := node.(map[string]any)
			if !ok {
				return
			}
			child = m[name]
		}
		if bracket == "[*]" {
			arr, ok := child.([]any)
			if !ok {
				return
			}
			for i, el := range arr {
				if tail == "" {
					arr[i] = repl
				} else {
					walkSet(el, tail, repl)
				}
			}
		}
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if tail == "" {
		m[key] = repl
		return
	}
	walkSet(m[key], tail, repl)
}

func recursiveSet(node any, key, tail, repl string) {
	switch n := node.(type) {
	case map[string]any:
		if tail == "" {
			if _, ok := n[key]; ok {
				n[key] = repl
			}
		} else if v, ok := n[key]; ok {
			walkSet(v, tail, repl)
		}
		for _, v := range n {
			recursiveSet(v, key, tail, repl)
		}
	case []any:
		for _, el := range n {
			recursiveSet(el, key, tail, repl)
		}
	}
}

// Scan runs the non-disableable secret scanner over the final serialized
// cassette bytes. Returns an error describing the first match found. Only
// matches that are not covered by any allowed pattern count as failures.
func Scan(cassetteJSON []byte, opts RedactOptions) error {
	var allowed []*regexp.Regexp
	for _, p := range opts.Allowed {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		allowed = append(allowed, re)
	}
	isAllowed := func(match string) bool {
		for _, re := range allowed {
			if re.MatchString(match) {
				return true
			}
		}
		return false
	}

	for _, re := range builtinScannerPatterns {
		for _, m := range re.FindAll(cassetteJSON, -1) {
			if isAllowed(string(m)) {
				continue
			}
			return fmt.Errorf("vcr: secret scanner matched %q (pattern %s)", string(m), re.String())
		}
	}
	for _, s := range opts.KnownSecrets {
		if s == "" {
			continue
		}
		if strings.Contains(string(cassetteJSON), s) {
			return fmt.Errorf("vcr: known secret leaked into cassette")
		}
	}
	return nil
}
