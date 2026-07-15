//go:build windows

package devtls

import (
	"context"
	"os"
	"testing"
)

func TestWindowsUserStoreCommandUsesTemporaryPublicFile(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"certutil.exe": `C:\Windows\System32\certutil.exe`}}
	store, err := newNativeStore(nativeStoreConfig{name: "test", scope: ScopeUser, runner: r})
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
	if len(r.calls) != 1 || r.calls[0].name != "certutil.exe" {
		t.Fatalf("calls=%#v", r.calls)
	}
	args := r.calls[0].args
	if len(args) != 5 || args[0] != "-user" || args[2] != "-addstore" {
		t.Fatalf("args=%v", args)
	}
	if _, err := os.Stat(args[4]); !os.IsNotExist(err) {
		t.Fatalf("temporary cert remains: %v", err)
	}
	if r.privateKeyObserved {
		t.Fatal("private key observed")
	}
}

func TestWindowsSystemStoreDoesNotAttemptUACWrapper(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"certutil.exe": `C:\Windows\System32\certutil.exe`}}
	store, err := newNativeStore(nativeStoreConfig{name: "test", scope: ScopeSystem, runner: r})
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
	if len(r.calls) != 1 || r.calls[0].name != "certutil.exe" || r.calls[0].args[0] != "-f" {
		t.Fatalf("calls=%#v", r.calls)
	}
}
