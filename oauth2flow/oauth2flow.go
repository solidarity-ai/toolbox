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

// AuthorizationStarter is optionally implemented by receivers that need the
// final state-bound authorization URL before waiting for a code.
type AuthorizationStarter interface {
	StartAuthorization(ctx context.Context, state string, authorizationURL string) error
}

// Run executes the OAuth2 authorization code flow:
//  1. Set cfg.RedirectURL from receiver
//  2. Build the authorization URL via cfg.AuthCodeURL
//  3. Start receiver authorization registration if needed
//  4. Call onAuthURL so the caller can print/open the URL
//  5. Wait for the code via receiver.ReceiveCode
//  6. Exchange the code for tokens via cfg.Exchange
func Run(ctx context.Context, cfg *oauth2.Config, receiver CodeReceiver, verifier string, opts []oauth2.AuthCodeOption, onAuthURL func(string)) (*oauth2.Token, error) {
	code, err := AuthorizeCode(ctx, cfg, receiver, verifier, opts, onAuthURL)
	if err != nil {
		return nil, err
	}

	var exchangeOpts []oauth2.AuthCodeOption
	if verifier != "" {
		exchangeOpts = append(exchangeOpts, oauth2.VerifierOption(verifier))
	}
	return cfg.Exchange(ctx, code, exchangeOpts...)
}

// AuthorizeCode runs the authorization URL + receiver portion of the flow and
// returns the authorization code before token exchange.
func AuthorizeCode(ctx context.Context, cfg *oauth2.Config, receiver CodeReceiver, verifier string, opts []oauth2.AuthCodeOption, onAuthURL func(string)) (string, error) {
	cfg.RedirectURL = receiver.RedirectURI()

	// Generate random state to prevent CSRF on the callback.
	var stateBytes [16]byte
	if _, err := rand.Read(stateBytes[:]); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes[:])

	authOpts := make([]oauth2.AuthCodeOption, 0, len(opts)+1)
	if verifier != "" {
		authOpts = append(authOpts, oauth2.S256ChallengeOption(verifier))
	}
	authOpts = append(authOpts, opts...)

	authURL := cfg.AuthCodeURL(state, authOpts...)
	if starter, ok := receiver.(AuthorizationStarter); ok {
		if err := starter.StartAuthorization(ctx, state, authURL); err != nil {
			return "", err
		}
	}
	onAuthURL(authURL)

	code, err := receiver.ReceiveCode(ctx, state)
	if err != nil {
		return "", err
	}

	return code, nil
}
