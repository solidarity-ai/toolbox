package vcr

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MatchOptions configures request matching.
type MatchOptions struct {
	IgnoreHeaders   []string
	IgnoreJSONPaths []string
}

// Match returns true if the live request matches the recorded request under
// the given options. Default match key: method + url + body.
func Match(live, recorded Request, opts MatchOptions) bool {
	if !strings.EqualFold(live.Method, recorded.Method) {
		return false
	}
	if live.URL != recorded.URL {
		return false
	}
	return matchBody(live.Body, recorded.Body, opts.IgnoreJSONPaths)
}

func matchBody(a, b json.RawMessage, ignorePaths []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	// Try JSON-aware compare when both sides parse.
	var av, bv any
	errA := json.Unmarshal(a, &av)
	errB := json.Unmarshal(b, &bv)
	if errA == nil && errB == nil {
		for _, p := range ignorePaths {
			stripJSONPath(av, p)
			stripJSONPath(bv, p)
		}
		return deepEqualJSON(av, bv)
	}
	return bytes.Equal(a, b)
}

func deepEqualJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// stripJSONPath removes the nodes addressed by path in place.
// Supported syntax:
//
//	$.foo.bar
//	$..id          (recursive)
//	$.items[*].name
func stripJSONPath(root any, path string) {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "$") {
		return
	}
	rest := strings.TrimPrefix(path, "$")
	walkStrip(root, rest, false)
}

func walkStrip(node any, rest string, recursive bool) {
	if rest == "" {
		return
	}
	if strings.HasPrefix(rest, "..") {
		key, tail := splitNext(rest[2:])
		recursiveStrip(node, key, tail)
		return
	}
	if strings.HasPrefix(rest, ".") {
		rest = rest[1:]
	}
	key, tail := splitNext(rest)
	if key == "" {
		return
	}
	// Array wildcard: items[*] or [*]
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
			if tail == "" {
				// Can't "delete" every element cleanly; clear array.
				for i := range arr {
					arr[i] = nil
				}
				return
			}
			for _, el := range arr {
				walkStrip(el, tail, false)
			}
		}
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if tail == "" {
		delete(m, key)
		return
	}
	walkStrip(m[key], tail, false)
}

func recursiveStrip(node any, key, tail string) {
	switch n := node.(type) {
	case map[string]any:
		if tail == "" {
			delete(n, key)
		} else if v, ok := n[key]; ok {
			walkStrip(v, tail, false)
		}
		for _, v := range n {
			recursiveStrip(v, key, tail)
		}
	case []any:
		for _, el := range n {
			recursiveStrip(el, key, tail)
		}
	}
}

func splitNext(s string) (head, tail string) {
	if s == "" {
		return "", ""
	}
	// head ends at next '.' (but keep brackets attached to head)
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				return s[:i], s[i:]
			}
		}
	}
	return s, ""
}
