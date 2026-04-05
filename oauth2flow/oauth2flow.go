package oauth2flow

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/oauth2"
)

// CodeReceiver receives an OAuth2 authorization code. Implementations
// handle the mechanism (local HTTP callback, manual paste, etc.).
type CodeReceiver interface {
	// RedirectURI returns the OAuth2 redirect URI this receiver listens on.
	RedirectURI() string
	// ReceiveCode blocks until an authorization code arrives or ctx is cancelled.
	// It validates that the returned state matches the expected value.
	ReceiveCode(ctx context.Context, expectedState string) (string, error)
	// Close releases any resources (e.g., stops an HTTP server).
	Close() error
}

// Run executes the OAuth2 authorization code flow:
//  1. Set cfg.RedirectURL from receiver
//  2. Build the authorization URL via cfg.AuthCodeURL
//  3. Call onAuthURL so the caller can print/open the URL
//  4. Wait for the code via receiver.ReceiveCode
//  5. Exchange the code for tokens via cfg.Exchange
func Run(ctx context.Context, cfg *oauth2.Config, receiver CodeReceiver, verifier string, opts []oauth2.AuthCodeOption, onAuthURL func(string)) (*oauth2.Token, error) {
	cfg.RedirectURL = receiver.RedirectURI()

	// Generate random state to prevent CSRF on the callback.
	var stateBytes [16]byte
	if _, err := rand.Read(stateBytes[:]); err != nil {
		return nil, err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes[:])

	authOpts := make([]oauth2.AuthCodeOption, 0, len(opts)+1)
	authOpts = append(authOpts, oauth2.S256ChallengeOption(verifier))
	authOpts = append(authOpts, opts...)

	authURL := cfg.AuthCodeURL(state, authOpts...)
	onAuthURL(authURL)

	code, err := receiver.ReceiveCode(ctx, state)
	if err != nil {
		return nil, err
	}

	return cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
}
