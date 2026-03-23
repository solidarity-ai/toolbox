package fetch

import (
	"io"
)

// Response implements the Fetch API Response interface.
// https://fetch.spec.whatwg.org/#response-class
type Response struct {
	status     int
	statusText string
	headers    *Headers
	body       io.ReadCloser
	url        string
	ok         bool
	redirected bool
}

// NewResponse creates a new Response.
func NewResponse(body io.ReadCloser, init *ResponseInit) *Response {
	r := &Response{
		status:     200,
		statusText: "OK",
		headers:    NewHeaders(),
		body:       body,
	}
	if init != nil {
		if init.Status != 0 {
			r.status = init.Status
			r.ok = init.Status >= 200 && init.Status < 300
		} else {
			r.ok = true
		}
		if init.StatusText != "" {
			r.statusText = init.StatusText
		}
		if init.Headers != nil {
			r.headers = init.Headers.Clone()
		}
	} else {
		r.ok = true
	}
	return r
}

// ResponseInit configures a new Response.
type ResponseInit struct {
	Status     int
	StatusText string
	Headers    *Headers
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

// Redirected returns whether this response is the result of a redirect.
func (r *Response) Redirected() bool { return r.redirected }

// Clone creates a copy of the response (body cannot be cloned).
func (r *Response) Clone() *Response {
	return &Response{
		status:     r.status,
		statusText: r.statusText,
		headers:    r.headers.Clone(),
		url:        r.url,
		ok:         r.ok,
		redirected: r.redirected,
	}
}
