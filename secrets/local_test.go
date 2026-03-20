package secrets_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/solidarity-ai/toolbox/secrets"
)

func writeIdentityFile(t *testing.T, dir string) (identityPath string) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(path, []byte(identity.String()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestStore(t *testing.T) (*secrets.LocalSecretStore, string) {
	t.Helper()
	dir := t.TempDir()
	identityPath := writeIdentityFile(t, dir)
	storePath := filepath.Join(dir, "secrets")
	store := secrets.NewLocalSecretStore(storePath, identityPath)
	return store, storePath
}

func TestLocalSecretStore_GetSetDelete(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

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
	store, _ := newTestStore(t)

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
	dir := t.TempDir()
	identityPath := writeIdentityFile(t, dir)
	storePath := filepath.Join(dir, "secrets")

	// Write with first store instance.
	store1 := secrets.NewLocalSecretStore(storePath, identityPath)
	if err := store1.Set(ctx, "persistent", []byte("value")); err != nil {
		t.Fatal(err)
	}

	// Read with a new store instance — verifies the data was flushed and can be decrypted.
	store2 := secrets.NewLocalSecretStore(storePath, identityPath)
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
	store, _ := newTestStore(t)

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

func TestLocalSecretStore_BadIdentityPath(t *testing.T) {
	ctx := context.Background()
	store := secrets.NewLocalSecretStore(
		filepath.Join(t.TempDir(), "secrets"),
		filepath.Join(t.TempDir(), "nonexistent-keys.txt"),
	)
	_, err := store.Get(ctx, "key")
	if err == nil {
		t.Fatal("expected error for missing identity file")
	}
}

func TestLocalSecretStore_StoreFilePermissions(t *testing.T) {
	ctx := context.Background()
	store, storePath := newTestStore(t)

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
	store, _ := newTestStore(t)

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
