package tswasmcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWasip2CLIProxyCommandEnvIncludesHostTranslations(t *testing.T) {
	etcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(etcDir, "ssl", "certs"), 0o755); err != nil {
		t.Fatalf("mkdir ssl certs: %v", err)
	}

	req := Request{
		WasmPath: "/tmp/http-client.wasm",
		Runtime:  "wasip2-cli",
		Env: map[string]string{
			"HTTPS_PROXY":   "http://proxy.toolbox.internal:9443",
			"HTTP_PROXY":    "http://proxy.toolbox.internal:9443",
			"SSL_CERT_FILE": "/etc/ssl/certs/ca-certificates.crt",
			"NO_PROXY":      "",
		},
		Mounts: []Mount{{HostPath: etcDir, GuestPath: "/etc"}},
	}

	env, err := commandEnv(req)
	if err != nil {
		t.Fatalf("commandEnv: %v", err)
	}

	captured := envSliceToMap(env)
	if got := captured["HTTPS_PROXY"]; got != "http://127.0.0.1:9443" {
		t.Fatalf("host HTTPS_PROXY = %q, want localhost translation", got)
	}
	if got := captured["HTTP_PROXY"]; got != "http://127.0.0.1:9443" {
		t.Fatalf("host HTTP_PROXY = %q, want localhost translation", got)
	}
	if got := captured["SSL_CERT_FILE"]; got != filepath.Join(etcDir, "ssl", "certs", "ca-certificates.crt") {
		t.Fatalf("host SSL_CERT_FILE = %q, want translated host path", got)
	}
	if got := captured[guestMountsControlVar]; got == "" {
		t.Fatalf("%s missing from command env", guestMountsControlVar)
	}
	if got := captured[guestEnvControlVar]; got == "" {
		t.Fatalf("%s missing from command env", guestEnvControlVar)
	}

	var guestEnv map[string]string
	if err := json.Unmarshal([]byte(captured[guestEnvControlVar]), &guestEnv); err != nil {
		t.Fatalf("decode guest env payload: %v", err)
	}
	if got := guestEnv["HTTPS_PROXY"]; got != "http://proxy.toolbox.internal:9443" {
		t.Fatalf("guest HTTPS_PROXY = %q, want virtual proxy hostname", got)
	}
	if got := guestEnv["SSL_CERT_FILE"]; got != "/etc/ssl/certs/ca-certificates.crt" {
		t.Fatalf("guest SSL_CERT_FILE = %q, want guest path", got)
	}
}

func TestWasip2CLIProxyTranslateGuestPathToHost(t *testing.T) {
	mounts := []Mount{{HostPath: "/tmp/toolbox-support", GuestPath: "/etc"}}
	translated, ok := translateGuestPathToHost("/etc/ssl/certs/ca-certificates.crt", mounts)
	if !ok {
		t.Fatal("expected guest path to translate")
	}
	want := filepath.Join("/tmp/toolbox-support", "ssl", "certs", "ca-certificates.crt")
	if translated != want {
		t.Fatalf("translated path = %q, want %q", translated, want)
	}
}

func envSliceToMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if found {
			out[key] = value
		}
	}
	return out
}
