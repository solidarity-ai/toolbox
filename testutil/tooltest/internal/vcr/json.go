package vcr

import (
	"encoding/json"
	"io"
)

// jsonEncoder returns an indented, HTML-safe encoder matching SaveCassette.
func jsonEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc
}
