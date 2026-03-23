package fetch

import "errors"

var (
	ErrInvalidHeaderName  = errors.New("TypeError: invalid header name")
	ErrInvalidHeaderValue = errors.New("TypeError: invalid header value")
)
