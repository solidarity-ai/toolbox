package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/secrets"
)

func TestSecretStoreSetupRequiredForUninitializedLocalStore(t *testing.T) {
	dir := t.TempDir()
	store := secrets.NewLocalSecretStore(filepath.Join(dir, "secrets"), filepath.Join(dir, "keys.txt"))
	service := NewSecretStoreService(store)

	setupRequired, err := service.SetupRequired(context.Background())
	if err != nil {
		t.Fatalf("SetupRequired(): %v", err)
	}
	if !setupRequired {
		t.Fatal("SetupRequired() = false, want true for an uninitialized local store")
	}

	locked, err := service.Locked(context.Background())
	if err != nil {
		t.Fatalf("Locked(): %v", err)
	}
	if !locked {
		t.Fatal("Locked() = false, want uninitialized local store to present as locked")
	}
}

func TestSecretStoreRecoveryCodesRequirePassphraseExceptRecoveryUnlock(t *testing.T) {
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")

	ctx := context.Background()
	dir := t.TempDir()
	store := secrets.NewLocalSecretStore(filepath.Join(dir, "secrets"), filepath.Join(dir, "keys.txt"))
	service := NewSecretStoreService(store)

	if _, err := service.SetupStore(ctx, "hunter2"); err != nil {
		t.Fatalf("SetupStore(): %v", err)
	}
	if _, err := service.RecoveryCodes(ctx, ""); !errors.Is(err, secrets.ErrLocked) {
		t.Fatalf("RecoveryCodes(empty) error = %v, want ErrLocked", err)
	}
	codes, err := service.RecoveryCodes(ctx, "hunter2")
	if err != nil {
		t.Fatalf("RecoveryCodes(passphrase): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if err := store.Unlock(ctx, codes[0]); err != nil {
		t.Fatalf("Unlock(recovery code): %v", err)
	}
	if _, err := service.RecoveryCodes(ctx, ""); err != nil {
		t.Fatalf("RecoveryCodes(recovery unlocked): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if _, err := service.RecoveryCodes(ctx, ""); !errors.Is(err, secrets.ErrLocked) {
		t.Fatalf("RecoveryCodes(after lock) error = %v, want ErrLocked", err)
	}
}
