package secrets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/solidarity-ai/toolbox/secrets"
)

const testUnlockKey = "test-secret-key"

func setTestScryptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	t.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")
}

func writeIdentityFile(t *testing.T, dir string) (identityPath string, recipient age.Recipient) {
	t.Helper()
	setTestScryptEnv(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keys.txt")
	content := []byte(identity.String() + "\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return path, identity.Recipient()
}

func newTestStore(t *testing.T) (*secrets.LocalSecretStore, string, string) {
	t.Helper()
	setTestScryptEnv(t)
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")
	store := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)
	if _, err := store.Setup(context.Background(), testUnlockKey); err != nil {
		t.Fatal(err)
	}
	return store, storePath, identityPath + ".age"
}

func writeLegacyStoreFile(t *testing.T, storePath string, recipient age.Recipient, data map[string][]byte) {
	t.Helper()
	plaintext, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(storePath), 0700); err != nil {
		t.Fatal(err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(storePath), ".legacy-store-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	encWriter, err := age.Encrypt(tmpFile, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encWriter.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := encWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, storePath); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSecretStore_GetSetDelete(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTestStore(t)

	// Get nonexistent key returns ErrNotFound.
	_, err := store.Get(ctx, "foo")
	if err != secrets.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Set and Get.
	if err := store.Set(ctx, "foo", []byte("bar")); err != nil {
		t.Fatal(err)
	}
	val, err := store.Get(ctx, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "bar" {
		t.Fatalf("expected %q, got %q", "bar", string(val))
	}

	// Overwrite.
	if err := store.Set(ctx, "foo", []byte("baz")); err != nil {
		t.Fatal(err)
	}
	val, err = store.Get(ctx, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "baz" {
		t.Fatalf("expected %q, got %q", "baz", string(val))
	}

	// Delete.
	if err := store.Delete(ctx, "foo"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(ctx, "foo")
	if err != secrets.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	// Delete nonexistent returns ErrNotFound.
	if err := store.Delete(ctx, "foo"); err != secrets.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLocalSecretStore_List(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTestStore(t)

	// Empty store.
	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty list, got %v", keys)
	}

	// Add some keys.
	for _, kv := range []struct{ k, v string }{
		{"system/ca_cert", "cert"},
		{"system/tls_key", "key"},
		{"tenant/acme/oauth", "token"},
		{"tenant/beta/oauth", "token2"},
	} {
		if err := store.Set(ctx, kv.k, []byte(kv.v)); err != nil {
			t.Fatal(err)
		}
	}

	// List all.
	keys, err = store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys, got %d: %v", len(keys), keys)
	}

	// List with prefix.
	keys, err = store.List(ctx, "system/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 system keys, got %d: %v", len(keys), keys)
	}

	// List with specific prefix.
	keys, err = store.List(ctx, "tenant/acme/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "tenant/acme/oauth" {
		t.Fatalf("expected [tenant/acme/oauth], got %v", keys)
	}

	// List with no matches.
	keys, err = store.List(ctx, "nonexistent/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty list, got %v", keys)
	}
}

func TestLocalSecretStore_Persistence(t *testing.T) {
	ctx := context.Background()
	setTestScryptEnv(t)
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")

	// Write with first store instance.
	store1 := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)
	if _, err := store1.Setup(ctx, testUnlockKey); err != nil {
		t.Fatal(err)
	}
	if err := store1.Set(ctx, "persistent", []byte("value")); err != nil {
		t.Fatal(err)
	}

	// Read with a new store instance — verifies the data was flushed and can be decrypted.
	store2 := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)
	val, err := store2.Get(ctx, "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "value" {
		t.Fatalf("expected %q, got %q", "value", string(val))
	}
}

func TestLocalSecretStore_InvalidKey(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTestStore(t)

	if _, err := store.Get(ctx, ""); err != secrets.ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
	if err := store.Set(ctx, "", []byte("v")); err != secrets.ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
	if err := store.Delete(ctx, ""); err != secrets.ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestLocalSecretStore_LockedWithoutKey(t *testing.T) {
	ctx := context.Background()
	setTestScryptEnv(t)
	dir := t.TempDir()
	store := secrets.NewLocalSecretStore(filepath.Join(dir, "secrets"), filepath.Join(dir, "keys.txt"))
	_, err := store.Get(ctx, "key")
	if err != secrets.ErrLocked {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

func TestLocalSecretStore_AutoSetupWritesBackupCodes(t *testing.T) {
	ctx := context.Background()
	setTestScryptEnv(t)
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")
	store := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)
	var out bytes.Buffer
	store.SetBackupCodeWriter(&out)

	if err := store.Set(ctx, "key", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Toolbox secret store backup codes:") || !strings.Contains(got, "TBX1-") {
		t.Fatalf("backup code output = %q, want heading and backup code", got)
	}
	if _, err := os.Stat(identityPath + ".recovery.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup-code sidecar Stat() error = %v, want os.ErrNotExist", err)
	}
}

func TestLocalSecretStore_AutoSetupWithoutWriterReturnsNotInitialized(t *testing.T) {
	ctx := context.Background()
	setTestScryptEnv(t)
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "keys.txt")
	storePath := filepath.Join(dir, "secrets")
	store := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)

	if err := store.Set(ctx, "key", []byte("value")); !errors.Is(err, secrets.ErrNotInitialized) {
		t.Fatalf("Set() error = %v, want ErrNotInitialized", err)
	}
	if _, err := os.Stat(storePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store Stat() error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(identityPath + ".age"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity Stat() error = %v, want os.ErrNotExist", err)
	}
}

func TestLocalSecretStore_StoreFilePermissions(t *testing.T) {
	ctx := context.Background()
	store, storePath, _ := newTestStore(t)

	if err := store.Set(ctx, "key", []byte("val")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Fatalf("expected store file permissions 0600, got %04o", perm)
	}
}

func TestLocalSecretStore_GetReturnsCopy(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTestStore(t)

	if err := store.Set(ctx, "key", []byte("original")); err != nil {
		t.Fatal(err)
	}

	val, err := store.Get(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the returned slice.
	val[0] = 'X'

	// The store's internal copy should be unaffected.
	val2, err := store.Get(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(val2) != "original" {
		t.Fatalf("store internal data was mutated: got %q", string(val2))
	}
}

func TestLocalSecretStore_CreatesWrappedIdentity(t *testing.T) {
	ctx := context.Background()
	store, _, wrappedIdentityPath := newTestStore(t)

	if err := store.Set(ctx, "key", []byte("value")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(wrappedIdentityPath)
	if err != nil {
		t.Fatalf("Stat(%q): %v", wrappedIdentityPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("wrapped identity permissions = %04o, want 0600", perm)
	}

	wrappedData, err := os.ReadFile(wrappedIdentityPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wrappedData, []byte("AGE-SECRET-KEY-")) {
		t.Fatal("wrapped identity file contains plaintext secret key material")
	}
}

func TestLocalSecretStore_ExistingStoreWithoutWrappedIdentityFails(t *testing.T) {
	ctx := context.Background()
	setTestScryptEnv(t)
	dir := t.TempDir()
	identityPath, recipient := writeIdentityFile(t, dir)
	storePath := filepath.Join(dir, "secrets")

	writeLegacyStoreFile(t, storePath, recipient, map[string][]byte{
		"legacy/token": []byte("value"),
	})

	store := secrets.NewLocalSecretStoreWithKey(storePath, identityPath, testUnlockKey)
	if _, err := store.Get(ctx, "legacy/token"); err == nil {
		t.Fatal("Get(legacy/token) error = nil, want missing wrapped identity error")
	} else if !strings.Contains(err.Error(), "opening wrapped identity file") {
		t.Fatalf("Get(legacy/token) error = %v, want wrapped identity error", err)
	}
}
