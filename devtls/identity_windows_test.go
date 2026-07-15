//go:build windows

package devtls

import (
	"bytes"
	"testing"
)

func TestWindowsIdentityUsesDPAPIRoundTrip(t *testing.T) {
	plain := []byte("leaf certificate and private key")
	protected, err := protectIdentity(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, plain) {
		t.Fatal("DPAPI identity contains plaintext")
	}
	got, err := unprotectIdentity(protected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip=%q, want %q", got, plain)
	}
}
