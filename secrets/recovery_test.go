package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const recoveryTestUnlockKey = "test-secret-key"

func setRecoveryTestScryptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")
}

func newRecoveryTestStore(t *testing.T) (*LocalSecretStore, string, string, string) {
	t.Helper()
	setRecoveryTestScryptEnv(t)
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")
	store := NewLocalSecretStoreWithKey(storePath, identityPath, recoveryTestUnlockKey)
	return store, storePath, identityPath + ".age", filepath.Join(dir, "keys.txt.recovery.json")
}

func TestLocalSecretStore_SetupCreatesStoreAndBackupCodes(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, recoveryPath := newRecoveryTestStore(t)

	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("len(codes) = %d, want 3", len(codes))
	}
	for _, code := range codes {
		normalized := normalizeRecoveryCode(code)
		if !strings.HasPrefix(normalized, backupCodeVersionPrefix) {
			t.Fatalf("backup code %q missing %s prefix", code, backupCodeVersionPrefix)
		}
		if _, err := decodeBackupCode(code); err != nil {
			t.Fatalf("decodeBackupCode(%q): %v", code, err)
		}
		if len(normalized) < 120 {
			t.Fatalf("backup code has %d characters, want at least 120", len(normalized))
		}
	}
	for _, path := range []string{storePath, identityPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Fatalf("%s perm = %04o, want 0600", path, perm)
		}
	}
	if _, err := os.Stat(recoveryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want os.ErrNotExist", recoveryPath, err)
	}
	if err := store.Set(ctx, "after/setup", []byte("ok")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
}

func TestLocalSecretStore_BackupCodesAreSelfContained(t *testing.T) {
	ctx := context.Background()
	store, _, _, recoveryPath := newRecoveryTestStore(t)
	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if _, err := os.Stat(recoveryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want os.ErrNotExist", recoveryPath, err)
	}
	identityText, err := decodeBackupCode(codes[0])
	if err != nil {
		t.Fatalf("decodeBackupCode(): %v", err)
	}
	if _, err := parseIdentityText(identityText); err != nil {
		t.Fatalf("parseIdentityText(decoded backup code): %v", err)
	}
}

func TestLocalSecretStore_BackupCodeUnlockWorks(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}

	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	codeInput := "  " + strings.ToLower(codes[0]) + "  "
	if err := recovered.Unlock(ctx, codeInput); err != nil {
		t.Fatalf("Unlock(backup code): %v", err)
	}
	value, err := recovered.Get(ctx, "service/token")
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if string(value) != "value" {
		t.Fatalf("Get() = %q, want value", value)
	}
}

func TestLocalSecretStore_BackupCodeUnlockCanReuseCode(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}

	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	if err := recovered.Unlock(ctx, codes[0]); err != nil {
		t.Fatalf("Unlock(first backup code): %v", err)
	}
	if err := recovered.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if err := recovered.Unlock(ctx, codes[0]); err != nil {
		t.Fatalf("Unlock(reused backup code): %v", err)
	}
}

func TestLocalSecretStore_RecoveryCodesGenerateAdditionalCodes(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	oldCodes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	newCodes, err := store.RecoveryCodes(ctx)
	if err != nil {
		t.Fatalf("RecoveryCodes(): %v", err)
	}
	if len(newCodes) != defaultRecoveryCodeCount {
		t.Fatalf("len(newCodes) = %d, want %d", len(newCodes), defaultRecoveryCodeCount)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}

	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	if err := recovered.Unlock(ctx, oldCodes[0]); err != nil {
		t.Fatalf("Unlock(old backup code): %v", err)
	}
	if err := recovered.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if err := recovered.Unlock(ctx, newCodes[0]); err != nil {
		t.Fatalf("Unlock(new backup code): %v", err)
	}
}

func TestLocalSecretStore_BackupCodeUnlockWorksWhenWrappedIdentityMissingOrCorrupted(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, path string)
	}{
		{name: "missing", edit: func(t *testing.T, path string) { t.Helper(); _ = os.Remove(path) }},
		{name: "corrupted", edit: func(t *testing.T, path string) { t.Helper(); os.WriteFile(path, []byte("not age"), 0600) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, storePath, identityPath, _ := newRecoveryTestStore(t)
			codes, err := store.Setup(ctx, recoveryTestUnlockKey)
			if err != nil {
				t.Fatalf("Setup(): %v", err)
			}
			if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
				t.Fatalf("Set(): %v", err)
			}
			if err := store.Lock(ctx); err != nil {
				t.Fatalf("Lock(): %v", err)
			}
			tc.edit(t, identityPath)

			recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
			if err := recovered.Unlock(ctx, codes[0]); err != nil {
				t.Fatalf("Unlock(backup code): %v", err)
			}
			value, err := recovered.Get(ctx, "service/token")
			if err != nil {
				t.Fatalf("Get(): %v", err)
			}
			if string(value) != "value" {
				t.Fatalf("Get() = %q, want value", value)
			}
		})
	}
}

func TestLocalSecretStore_InvalidBackupCodeFails(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	_, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	if err := recovered.Unlock(ctx, "WRONG-BACKUP-CODE"); err == nil {
		t.Fatal("Unlock(invalid backup code) error = nil, want error")
	}
}

func TestLocalSecretStore_RewrapAfterRecoveryAllowsNewPassphrase(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}

	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	if err := recovered.Unlock(ctx, codes[0]); err != nil {
		t.Fatalf("Unlock(backup code): %v", err)
	}
	if err := recovered.RewrapAfterRecovery(ctx, "new-secret-key"); err != nil {
		t.Fatalf("RewrapAfterRecovery(): %v", err)
	}
	if err := recovered.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}

	withNewKey := NewLocalSecretStoreWithKey(storePath, strings.TrimSuffix(identityPath, ".age"), "new-secret-key")
	value, err := withNewKey.Get(ctx, "service/token")
	if err != nil {
		t.Fatalf("Get() with new key: %v", err)
	}
	if string(value) != "value" {
		t.Fatalf("Get() = %q, want value", value)
	}
}

func TestLocalSecretStore_RewrapAfterRecoveryExpiredWindow(t *testing.T) {
	ctx := context.Background()
	store, storePath, identityPath, _ := newRecoveryTestStore(t)
	codes, err := store.Setup(ctx, recoveryTestUnlockKey)
	if err != nil {
		t.Fatalf("Setup(): %v", err)
	}
	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	recovered := NewLocalSecretStore(storePath, strings.TrimSuffix(identityPath, ".age"))
	if err := recovered.Unlock(ctx, codes[0]); err != nil {
		t.Fatalf("Unlock(backup code): %v", err)
	}

	oldNow := timeNow
	t.Cleanup(func() { timeNow = oldNow })
	timeNow = func() time.Time { return oldNow().Add(10 * time.Minute) }
	if err := recovered.RewrapAfterRecovery(ctx, "new-secret-key"); !errors.Is(err, ErrRecoveryWindowExpired) {
		t.Fatalf("RewrapAfterRecovery() error = %v, want ErrRecoveryWindowExpired", err)
	}
}
