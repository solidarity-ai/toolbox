package fetch

import (
	"io"
)

// Response holds the result of a Fetch call.
type Response struct {
	status     int
	statusText string
	headers    *Headers
	body       io.ReadCloser
	url        string
	ok         bool
	redirected bool
}

// Status returns the HTTP status code.
func (r *Response) Status() int { return r.status }

// StatusText returns the HTTP status text.
func (r *Response) StatusText() string { return r.statusText }

// Headers returns the response headers.
func (r *Response) Headers() *Headers { return r.headers }

// Body returns the response body.
func (r *Response) Body() io.ReadCloser { return r.body }

// OK returns true if status is in 200-299 range.
func (r *Response) OK() bool { return r.ok }

// URL returns the response URL.
func (r *Response) URL() string { return r.url }
