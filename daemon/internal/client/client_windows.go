//go:build windows

package client

func EnsureConnection() (*Client, error) {
	return nil, ErrUnsupportedPlatform
}

func (c *Client) Ping() (PingResult, error) {
	return PingResult{}, ErrUnsupportedPlatform
}

func (c *Client) Close() error {
	return ErrUnsupportedPlatform
}
