package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/secrets"
)

func TestDaemonPreferredSecretStoreFallsBackForLifecycleOperations(t *testing.T) {
	ctx := context.Background()
	fallback := &recordingLifecycleStore{}
	store := &daemonPreferredSecretStore{
		primary:  unsupportedLifecycleStore{},
		fallback: fallback,
	}

	if initialized, err := store.Initialized(); err != nil || !initialized {
		t.Fatalf("Initialized() = %v, %v, want true, nil", initialized, err)
	}
	if _, err := store.Setup(ctx, "setup-key"); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	if err := store.Unlock(ctx, "unlock-key"); err != nil {
		t.Fatalf("Unlock() error: %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock() error: %v", err)
	}
	if _, err := store.BackupCodes(ctx, "backup-key"); err != nil {
		t.Fatalf("BackupCodes() error: %v", err)
	}
	if _, err := store.RecoveryCodes(ctx); err != nil {
		t.Fatalf("RecoveryCodes() error: %v", err)
	}
	if unlocked, err := store.RecoveryUnlocked(ctx); err != nil || !unlocked {
		t.Fatalf("RecoveryUnlocked() = %v, %v, want true, nil", unlocked, err)
	}
	if err := store.RewrapAfterRecovery(ctx, "new-key"); err != nil {
		t.Fatalf("RewrapAfterRecovery() error: %v", err)
	}

	want := []string{"initialized", "setup:setup-key", "unlock:unlock-key", "lock", "backup:backup-key", "recovery-codes", "recovery-unlocked", "rewrap:new-key"}
	if !reflect.DeepEqual(fallback.calls, want) {
		t.Fatalf("fallback calls = %#v, want %#v", fallback.calls, want)
	}
}

type unsupportedLifecycleStore struct{}

func (unsupportedLifecycleStore) Get(context.Context, string) ([]byte, error) {
	return nil, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Set(context.Context, string, []byte) error {
	return daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Delete(context.Context, string) error {
	return daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) List(context.Context, string) ([]string, error) {
	return nil, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Unlock(context.Context, string) error {
	return daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Lock(context.Context) error {
	return daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Initialized() (bool, error) {
	return false, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) Setup(context.Context, string) ([]string, error) {
	return nil, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) BackupCodes(context.Context, string) ([]string, error) {
	return nil, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) RecoveryCodes(context.Context) ([]string, error) {
	return nil, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) RecoveryUnlocked(context.Context) (bool, error) {
	return false, daemon.ErrUnsupportedPlatform
}

func (unsupportedLifecycleStore) RewrapAfterRecovery(context.Context, string) error {
	return daemon.ErrUnsupportedPlatform
}

type recordingLifecycleStore struct {
	calls []string
}

var _ secrets.LifecycleSecretStore = (*recordingLifecycleStore)(nil)

func (s *recordingLifecycleStore) Get(context.Context, string) ([]byte, error) {
	return []byte("value"), nil
}

func (s *recordingLifecycleStore) Set(context.Context, string, []byte) error {
	return nil
}

func (s *recordingLifecycleStore) Delete(context.Context, string) error {
	return nil
}

func (s *recordingLifecycleStore) List(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *recordingLifecycleStore) Unlock(_ context.Context, key string) error {
	s.calls = append(s.calls, "unlock:"+key)
	return nil
}

func (s *recordingLifecycleStore) Lock(context.Context) error {
	s.calls = append(s.calls, "lock")
	return nil
}

func (s *recordingLifecycleStore) Initialized() (bool, error) {
	s.calls = append(s.calls, "initialized")
	return true, nil
}

func (s *recordingLifecycleStore) Setup(_ context.Context, key string) ([]string, error) {
	s.calls = append(s.calls, "setup:"+key)
	return []string{"code-1"}, nil
}

func (s *recordingLifecycleStore) BackupCodes(_ context.Context, key string) ([]string, error) {
	s.calls = append(s.calls, "backup:"+key)
	return []string{"code-2"}, nil
}

func (s *recordingLifecycleStore) RecoveryCodes(context.Context) ([]string, error) {
	s.calls = append(s.calls, "recovery-codes")
	return []string{"code-3"}, nil
}

func (s *recordingLifecycleStore) RecoveryUnlocked(context.Context) (bool, error) {
	s.calls = append(s.calls, "recovery-unlocked")
	return true, nil
}

func (s *recordingLifecycleStore) RewrapAfterRecovery(_ context.Context, key string) error {
	s.calls = append(s.calls, "rewrap:"+key)
	return nil
}
