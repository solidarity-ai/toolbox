package codemodesdks

import (
	"strings"
	"testing"
)

func TestWriteCallableIntrinsicAlias(t *testing.T) {
	var got strings.Builder
	writeCallableIntrinsicAlias(&got, "  ", "name")
	if declaration := "_name(): string;"; !strings.Contains(got.String(), declaration) {
		t.Fatalf("intrinsic alias missing %q:\n%s", declaration, got.String())
	}
}

func TestCallableIntrinsicAliasSignatures(t *testing.T) {
	tests := map[string]string{
		"apply":       "(thisArg: unknown, args?: unknown[]): unknown",
		"bind":        "(thisArg: unknown, ...args: unknown[]): (...args: unknown[]) => unknown",
		"caller":      "(...args: unknown[]): unknown",
		"arguments":   "(...args: unknown[]): unknown",
		"constructor": "(...args: unknown[]): unknown",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := callableIntrinsicAliasSignature(name); got != want {
				t.Fatalf("callableIntrinsicAliasSignature(%q) = %q, want %q", name, got, want)
			}
		})
	}
}
