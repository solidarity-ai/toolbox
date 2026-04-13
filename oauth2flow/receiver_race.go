package oauth2flow

import (
	"context"
	"errors"
	"fmt"
)

// RaceReceiver runs multiple CodeReceivers concurrently and returns the
// first code received. All receivers share the same redirect URI.
type RaceReceiver struct {
	receivers   []CodeReceiver
	redirectURI string
}

// NewRaceReceiver creates a RaceReceiver. All receivers must share the
// given redirectURI.
func NewRaceReceiver(redirectURI string, receivers ...CodeReceiver) *RaceReceiver {
	return &RaceReceiver{receivers: receivers, redirectURI: redirectURI}
}

// RedirectURI returns the shared redirect URI.
func (r *RaceReceiver) RedirectURI() string { return r.redirectURI }

// ReceiveCode launches all receivers concurrently and returns the first
// successful code. If all receivers fail, returns the last error.
func (r *RaceReceiver) ReceiveCode(ctx context.Context, expectedState string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, len(r.receivers))

	for _, recv := range r.receivers {
		go func(recv CodeReceiver) {
			code, err := recv.ReceiveCode(ctx, expectedState)
			ch <- result{code, err}
		}(recv)
	}

	var lastErr error
	for range r.receivers {
		res := <-ch
		if res.err == nil {
			return res.code, nil
		}
		var providerErr *providerAuthorizationError
		if errors.As(res.err, &providerErr) {
			return "", res.err
		}
		lastErr = res.err
	}
	return "", fmt.Errorf("oauth2flow: all receivers failed: %w", lastErr)
}

// Close closes all receivers.
func (r *RaceReceiver) Close() error {
	var firstErr error
	for _, recv := range r.receivers {
		if err := recv.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
