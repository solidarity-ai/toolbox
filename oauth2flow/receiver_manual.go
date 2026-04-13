package oauth2flow

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
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
func (r *ManualReceiver) ReceiveCode(ctx context.Context, expectedState string) (string, error) {
	if lineReader, ok := r.reader.(interface {
		ReadLine(context.Context) (string, error)
	}); ok {
		line, err := lineReader.ReadLine(ctx)
		if err != nil {
			return "", err
		}
		return NormalizeAuthorizationCodeInput(line, expectedState)
	}

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r.reader)
		if scanner.Scan() {
			code, err := NormalizeAuthorizationCodeInput(scanner.Text(), expectedState)
			if err != nil {
				ch <- result{err: err}
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

// NormalizeAuthorizationCodeInput accepts either a raw OAuth2 authorization
// code or a full redirect URL and returns the authorization code. If the input
// is a redirect URL and includes state, the state must match expectedState.
func NormalizeAuthorizationCodeInput(line, expectedState string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("oauth2flow: empty authorization code")
	}

	if code, ok, err := extractAuthorizationCodeFromURL(line, expectedState); ok || err != nil {
		return code, err
	}
	return line, nil
}

func extractAuthorizationCodeFromURL(line, expectedState string) (string, bool, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return "", false, nil
	}
	if parsed.Scheme == "" {
		return "", false, nil
	}
	if parsed.Host == "" && !strings.EqualFold(parsed.Scheme, "urn") {
		return "", false, nil
	}

	if code, matched, err := parseAuthorizationResponseValues(parsed.Query(), expectedState); matched || err != nil {
		return code, matched, err
	}
	if parsed.Fragment != "" {
		fragmentValues, err := url.ParseQuery(parsed.Fragment)
		if err == nil {
			return parseAuthorizationResponseValues(fragmentValues, expectedState)
		}
	}
	return "", false, nil
}

type providerAuthorizationError struct {
	code        string
	description string
	uri         string
}

func (e *providerAuthorizationError) Error() string {
	msg := fmt.Sprintf("oauth2flow: provider returned error %s", e.code)
	if e.description != "" {
		msg += ": " + e.description
	}
	if e.uri != "" {
		msg += " (" + e.uri + ")"
	}
	return msg
}

func parseAuthorizationResponseValues(values url.Values, expectedState string) (string, bool, error) {
	code := values.Get("code")
	errorCode := values.Get("error")
	state := values.Get("state")
	if code == "" && errorCode == "" {
		return "", false, nil
	}
	if expectedState != "" && state != "" && state != expectedState {
		return "", true, fmt.Errorf("oauth2flow: state mismatch (possible CSRF)")
	}
	if errorCode != "" {
		return "", true, &providerAuthorizationError{
			code:        errorCode,
			description: values.Get("error_description"),
			uri:         values.Get("error_uri"),
		}
	}
	return code, true, nil
}

// Close is a no-op.
func (r *ManualReceiver) Close() error { return nil }
