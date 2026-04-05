package oauth2flow

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const oobRedirectURI = "urn:ietf:wg:oauth:2.0:oob"

// ManualReceiver reads an authorization code from an io.Reader (typically
// stdin). The user copies the code from the browser and pastes it.
type ManualReceiver struct {
	redirectURI string
	reader      io.Reader
}

// NewManualReceiver creates a ManualReceiver. If overrideRedirectURI is
// empty, uses the OOB redirect URI (for headless-only mode). When used
// alongside a CallbackReceiver, pass the callback's RedirectURI so both
// share the same redirect.
func NewManualReceiver(reader io.Reader, overrideRedirectURI string) *ManualReceiver {
	uri := overrideRedirectURI
	if uri == "" {
		uri = oobRedirectURI
	}
	return &ManualReceiver{redirectURI: uri, reader: reader}
}

// RedirectURI returns the configured redirect URI.
func (r *ManualReceiver) RedirectURI() string { return r.redirectURI }

// ReceiveCode reads one line from the reader. If the reader also supports
// ReadLine(context.Context), that path is used so cancellations do not leave
// behind blocked reads on a shared input stream. Otherwise it falls back to a
// background scanner goroutine around the raw io.Reader.
func (r *ManualReceiver) ReceiveCode(ctx context.Context, _ string) (string, error) {
	if lineReader, ok := r.reader.(interface {
		ReadLine(context.Context) (string, error)
	}); ok {
		code, err := lineReader.ReadLine(ctx)
		if err != nil {
			return "", err
		}
		if code == "" {
			return "", fmt.Errorf("oauth2flow: empty authorization code")
		}
		return code, nil
	}

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r.reader)
		if scanner.Scan() {
			code := strings.TrimSpace(scanner.Text())
			if code == "" {
				ch <- result{err: fmt.Errorf("oauth2flow: empty authorization code")}
				return
			}
			ch <- result{code: code}
			return
		}
		if err := scanner.Err(); err != nil {
			ch <- result{err: fmt.Errorf("oauth2flow: reading code: %w", err)}
			return
		}
		ch <- result{err: fmt.Errorf("oauth2flow: no input")}
	}()

	select {
	case res := <-ch:
		return res.code, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close is a no-op.
func (r *ManualReceiver) Close() error { return nil }
