//go:build linux

package devtls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxDebianStoreCommandsUseTemporaryPublicFile(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"update-ca-certificates": "/usr/sbin/update-ca-certificates", "sudo": "/usr/bin/sudo"}}
	store, err := newNativeStore(nativeStoreConfig{name: "My Dev CA", scope: ScopeSystem, runner: r})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Install(context.Background(), bundle.RootCertPEM); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 || r.calls[0].name != "/usr/bin/sudo" || r.calls[1].name != "/usr/bin/sudo" {
		t.Fatalf("calls=%#v", r.calls)
	}
	path := r.calls[0].args[3]
	if filepath.Base(r.calls[0].args[4]) != "my-dev-ca.crt" {
		t.Fatalf("anchor=%v", r.calls[0].args)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary cert remains: %v", err)
	}
	if r.privateKeyObserved {
		t.Fatal("private key observed")
	}
}

func TestLinuxUserScopeIsUnsupported(t *testing.T) {
	_, err := newNativeStore(nativeStoreConfig{name: "test", scope: ScopeUser, runner: &fakeRunner{}})
	if err == nil {
		t.Fatal("ScopeUser succeeded")
	}
}
