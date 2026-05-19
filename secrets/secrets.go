package secrets

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a key does not exist in the store.
var ErrNotFound = errors.New("secret not found")

// ErrInvalidKey is returned when a key is empty.
var ErrInvalidKey = errors.New("invalid secret key")

// ErrLocked is returned when a secret store requires an unlock key.
var ErrLocked = errors.New("secret store is locked")

// ErrNotInitialized is returned when a secret store has not been set up yet.
var ErrNotInitialized = errors.New("secret store is not initialized")

// ErrRecoveryWindowExpired is returned when a recovery-only operation is no longer allowed.
var ErrRecoveryWindowExpired = errors.New("secret store recovery window expired")

// ErrRecoveryRequired is returned when an operation is only allowed after backup-code unlock.
var ErrRecoveryRequired = errors.New("secret store backup-code unlock required")

// SecretStore provides access to secrets by key.
type SecretStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// ManagedSecretStore supports explicit lock and unlock operations.
type ManagedSecretStore interface {
	SecretStore
	Unlock(ctx context.Context, unlockKey string) error
	Lock(ctx context.Context) error
}

// LifecycleSecretStore exposes explicit setup and recovery operations.
//
// Implementations must not perform first-time setup from ordinary Get/Set/List
// calls. Setup is intentionally modeled separately so product surfaces can show
// recovery codes at the time they are created.
type LifecycleSecretStore interface {
	ManagedSecretStore
	Initialized() (bool, error)
	Setup(ctx context.Context, unlockKey string) ([]string, error)
	BackupCodes(ctx context.Context, unlockKey string) ([]string, error)
	RecoveryCodes(ctx context.Context) ([]string, error)
	RecoveryUnlocked(ctx context.Context) (bool, error)
	RewrapAfterRecovery(ctx context.Context, newUnlockKey string) error
}

func validateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	return nil
}
