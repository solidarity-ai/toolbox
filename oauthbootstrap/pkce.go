package oauthbootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	defaultStateBytes   = 24
	defaultPKCEBytes    = 32
	pkceChallengeMethod = "S256"
)

func randomToken(reader io.Reader, byteLen int) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func newState(reader io.Reader) (string, error) {
	return randomToken(reader, defaultStateBytes)
}

func newPKCEVerifier(reader io.Reader) (string, error) {
	return randomToken(reader, defaultPKCEBytes)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
