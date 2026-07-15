//go:build darwin

package devtls

import (
	"context"
	"os"
	"testing"
)

func TestDarwinUserStoreUsesOnlyTemporaryPublicCertificateFiles(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"security": "/usr/bin/security"}}
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
	if len(r.calls) != 1 {
		t.Fatalf("calls=%#v", r.calls)
	}
	args := r.calls[0].args
	if len(args) != 6 || args[0] != "add-trusted-cert" || args[5] == "" {
		t.Fatalf("args=%v", args)
	}
	if _, err := os.Stat(args[5]); !os.IsNotExist(err) {
		t.Fatalf("temporary cert remains: %v", err)
	}
	if r.privateKeyObserved {
		t.Fatal("private key was written or passed to native command")
	}

	if err := store.Remove(context.Background(), bundle.RootCertPEM); err != nil {
		t.Fatal(err)
	}
	id := certFingerprint(t, bundle.RootCertPEM)
	last := r.calls[len(r.calls)-1]
	if last.name != "/usr/bin/security" || len(last.args) != 4 || last.args[0] != "delete-certificate" || last.args[3] != id {
		t.Fatalf("remove call=%#v", last)
	}
}

func TestDarwinSystemStoreUsesSudo(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"security": "/usr/bin/security", "sudo": "/usr/bin/sudo"}}
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
	if len(r.calls) != 1 || r.calls[0].name != "/usr/bin/sudo" {
		t.Fatalf("calls=%#v", r.calls)
	}
}

func TestDarwinTrustedUsesSSLHostnamePolicy(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"security": "/usr/bin/security"}}
	store, err := newNativeStore(nativeStoreConfig{name: "test", scope: ScopeUser, runner: r})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := store.Trusted(context.Background(), bundle.RootCertPEM, bundle.CertPEM, "localhost")
	if err != nil || !trusted {
		t.Fatalf("trusted=%v err=%v", trusted, err)
	}
	args := r.calls[0].args
	if len(args) != 9 || args[0] != "verify-cert" || args[4] != "ssl" || args[6] != "localhost" {
		t.Fatalf("args=%v", args)
	}
}
