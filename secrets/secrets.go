package secrets

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a key does not exist in the store.
var ErrNotFound = errors.New("secret not found")

// ErrInvalidKey is returned when a key is empty.
var ErrInvalidKey = errors.New("invalid secret key")

// SecretStore provides access to secrets by key.
type SecretStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

func validateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	return nil
}
