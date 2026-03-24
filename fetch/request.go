package fetch

import (
	"io"
	"strings"
)

// Request implements the Fetch API Request interface.
// https://fetch.spec.whatwg.org/#request-class
type Request struct {
	url     string
	method  string
	headers *Headers
	body    io.Reader
}

// RequestInit configures a new Request.
type RequestInit struct {
	Method  string
	Headers *Headers
	Body    io.Reader
}

// NewRequest creates a new Request for the given URL with optional init.
func NewRequest(url string, init *RequestInit) *Request {
	r := &Request{
		url:     url,
		method:  "GET",
		headers: NewHeaders(),
	}
	if init != nil {
		if init.Method != "" {
			r.method = normalizeMethod(init.Method)
		}
		if init.Headers != nil {
			r.headers = init.Headers.Clone()
		}
		if init.Body != nil {
			r.body = init.Body
		}
	}
	return r
}

// normalizeMethod uppercases standard HTTP methods per the Fetch spec.
func normalizeMethod(method string) string {
	upper := strings.ToUpper(method)
	switch upper {
	case "DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT":
		return upper
	default:
		return method
	}
}
