package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/solidarity-ai/toolbox/oauthbootstrap"
	"github.com/solidarity-ai/toolbox/secrets"
)

func TestRunAuthHelpIncludesUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "toolbox auth [--tenant TENANT] [PACKAGE_DIR]") {
		t.Fatalf("help output = %q, want auth usage", stdout.String())
	}
}

func TestRunAuthPromptsAndUsesTenantScope(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var gotReq oauthbootstrap.Request
	var openerURL string

	deps := authDeps{
		prompt: promptSecret,
		newStore: func() (secrets.SecretStore, error) {
			return secrets.NewLocalSecretStore(filepath.Join(t.TempDir(), "secrets.age"), writeAuthIdentityFile(t, t.TempDir())), nil
		},
		openBrowser: func(ctx context.Context, rawURL string) error {
			openerURL = rawURL
			return nil
		},
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			if _, ok := store.(*secrets.LocalSecretStore); !ok {
				t.Fatalf("store type = %T, want *secrets.LocalSecretStore", store)
			}
			return func(ctx context.Context, req oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				gotReq = req
				if err := opener(ctx, "https://accounts.example.com/oauth/authorize?client_id=test"); err != nil {
					return oauthbootstrap.Result{}, err
				}
				return oauthbootstrap.Result{
					CredentialName:  req.CredentialName,
					SecretNamespace: "example.com/acme/authpkg/tenant/acme/workspace",
					PersistedKeys:   []string{"client_id", "refresh_token"},
					UsedPKCE:        req.ClientSecret == "",
				}, nil
			}
		},
	}

	stdin := strings.NewReader("client-123\n\n")
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{"--tenant", "acme", packageDir}, stdin, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("runAuthWithDeps() error: %v\nstderr=%s", err, stderr.String())
	}
	if gotReq.Package.Dir != packageDir {
		t.Fatalf("package dir = %q, want %q", gotReq.Package.Dir, packageDir)
	}
	if gotReq.CredentialName != "workspace" {
		t.Fatalf("credential = %q, want workspace", gotReq.CredentialName)
	}
	if gotReq.ClientID != "client-123" {
		t.Fatalf("client id = %q, want client-123", gotReq.ClientID)
	}
	if gotReq.ClientSecret != "" {
		t.Fatalf("client secret = %q, want blank optional secret", gotReq.ClientSecret)
	}
	if gotReq.Tenant != "acme" {
		t.Fatalf("tenant = %q, want acme", gotReq.Tenant)
	}
	if openerURL == "" {
		t.Fatal("browser opener was not invoked")
	}
	if !strings.Contains(stdout.String(), `authorized oauth2 credential "workspace"`) {
		t.Fatalf("stdout = %q, want credential success output", stdout.String())
	}
	if !strings.Contains(stdout.String(), `tenant "acme" scope`) {
		t.Fatalf("stdout = %q, want tenant scope output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "public-client PKCE flow") {
		t.Fatalf("stdout = %q, want public-client PKCE output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "OAuth client_id") || !strings.Contains(stderr.String(), "OAuth client_secret") {
		t.Fatalf("stderr = %q, want both prompts", stderr.String())
	}
}

func TestRunAuthLoadsCurrentDirectoryByDefault(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var gotReq oauthbootstrap.Request

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(packageDir); err != nil {
		t.Fatalf("Chdir(%q): %v", packageDir, err)
	}
	defer os.Chdir(oldWD)

	deps := authDeps{
		prompt: promptSecret,
		newStore: func() (secrets.SecretStore, error) {
			return &stubSecretStore{}, nil
		},
		openBrowser: func(context.Context, string) error { return nil },
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			return func(ctx context.Context, req oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				gotReq = req
				return oauthbootstrap.Result{CredentialName: req.CredentialName, SecretNamespace: "example.com/acme/authpkg/workspace", PersistedKeys: []string{"client_id", "refresh_token"}}, nil
			}
		},
	}

	var stdout, stderr bytes.Buffer
	err = runAuthWithDeps(nil, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("runAuthWithDeps() error: %v\nstderr=%s", err, stderr.String())
	}
	if gotReq.Package.Dir != "." {
		t.Fatalf("package dir = %q, want . for default current-directory load", gotReq.Package.Dir)
	}
	if gotReq.ClientSecret != "secret-456" {
		t.Fatalf("client secret = %q, want explicit secret", gotReq.ClientSecret)
	}
	if !strings.Contains(stdout.String(), "package scope") {
		t.Fatalf("stdout = %q, want package scope output", stdout.String())
	}
}

func TestRunAuthRejectsInvalidPackagePath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{filepath.Join(t.TempDir(), "missing")}, strings.NewReader("unused\n"), &stdout, &stderr, defaultAuthDeps)
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want invalid manifest path error")
	}
	if !strings.Contains(err.Error(), "auth: package load failed") {
		t.Fatalf("error = %v, want package-load context", err)
	}
}

func TestRunAuthRejectsMalformedTenant(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{"--tenant", "bad/name", packageDir}, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, defaultAuthDeps)
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want malformed tenant error")
	}
	if !strings.Contains(err.Error(), `auth: invalid --tenant "bad/name"`) {
		t.Fatalf("error = %v, want invalid --tenant context", err)
	}
}

func TestRunAuthRejectsAmbiguousOAuthCredentials(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{extraOAuthCredential: true})
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{packageDir}, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, defaultAuthDeps)
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want ambiguous credential error")
	}
	if !strings.Contains(err.Error(), "declares multiple oauth2 credentials") {
		t.Fatalf("error = %v, want ambiguous credential context", err)
	}
}

func TestRunAuthRejectsBlankRequiredClientID(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{packageDir}, strings.NewReader("\nsecret-456\n"), &stdout, &stderr, authDeps{
		prompt:      promptSecret,
		newStore:    defaultAuthDeps.newStore,
		openBrowser: func(context.Context, string) error { return nil },
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			return func(context.Context, oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				t.Fatal("bootstrap should not run when required client_id prompt is blank")
				return oauthbootstrap.Result{}, nil
			}
		},
	})
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want prompt failure")
	}
	if !strings.Contains(err.Error(), "prompt collection failed") || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("error = %v, want prompt-stage client_id context", err)
	}
}

func TestRunAuthBubblesBrowserLaunchFailure(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{packageDir}, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, authDeps{
		prompt: promptSecret,
		newStore: func() (secrets.SecretStore, error) {
			return &stubSecretStore{}, nil
		},
		openBrowser: func(context.Context, string) error {
			return fmt.Errorf("desktop opener unavailable")
		},
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			return func(ctx context.Context, req oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				if err := opener(ctx, "https://accounts.example.com/oauth/authorize"); err != nil {
					return oauthbootstrap.Result{}, &oauthbootstrap.StageError{Stage: "browser launch", Err: err}
				}
				return oauthbootstrap.Result{}, nil
			}
		},
	})
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want browser-launch failure")
	}
	if !strings.Contains(err.Error(), "auth: browser launch failed") || !strings.Contains(err.Error(), "desktop opener unavailable") {
		t.Fatalf("error = %v, want browser-launch context", err)
	}
}

func TestRunAuthBubblesBootstrapStageFailure(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{packageDir}, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, authDeps{
		prompt: promptSecret,
		newStore: func() (secrets.SecretStore, error) {
			return &stubSecretStore{}, nil
		},
		openBrowser: func(context.Context, string) error { return nil },
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			return func(context.Context, oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				return oauthbootstrap.Result{}, &oauthbootstrap.StageError{Stage: "token exchange", Err: fmt.Errorf("provider token endpoint returned status 500")}
			}
		},
	})
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "auth: token exchange failed") || !strings.Contains(err.Error(), "path ") {
		t.Fatalf("error = %v, want token-exchange context", err)
	}
}

func TestRunAuthLocalSecretStoreUnlockFailure(t *testing.T) {
	packageDir := newOAuthPackageDir(t, authPackageOptions{})
	missingIdentity := filepath.Join(t.TempDir(), "missing-keys.txt")
	storePath := filepath.Join(t.TempDir(), "secrets.age")

	deps := authDeps{
		prompt: promptSecret,
		newStore: func() (secrets.SecretStore, error) {
			return secrets.NewLocalSecretStore(storePath, missingIdentity), nil
		},
		openBrowser: func(context.Context, string) error { return nil },
		newBootstrap: func(store secrets.SecretStore, opener oauthbootstrap.BrowserOpener) authBootstrapFunc {
			return func(ctx context.Context, req oauthbootstrap.Request) (oauthbootstrap.Result, error) {
				if _, err := store.List(ctx, ""); err != nil {
					return oauthbootstrap.Result{}, &oauthbootstrap.StageError{Stage: "secret persistence", Err: fmt.Errorf("unlock local secret store: %w", err)}
				}
				return oauthbootstrap.Result{}, nil
			}
		},
	}

	var stdout, stderr bytes.Buffer
	err := runAuthWithDeps([]string{packageDir}, strings.NewReader("client-123\nsecret-456\n"), &stdout, &stderr, deps)
	if err == nil {
		t.Fatal("runAuthWithDeps() error = nil, want local secret-store unlock failure")
	}
	if !strings.Contains(err.Error(), "auth: secret persistence failed") || !strings.Contains(err.Error(), "unlock local secret store") {
		t.Fatalf("error = %v, want secret-persistence context", err)
	}
	if strings.Contains(stdout.String(), "authorized oauth2 credential") {
		t.Fatalf("stdout = %q, want no success output on store failure", stdout.String())
	}
}

type authPackageOptions struct {
	extraOAuthCredential bool
}

func newOAuthPackageDir(t *testing.T, opts authPackageOptions) string {
	t.Helper()
	dir := t.TempDir()
	manifest := map[string]any{
		"module":  "example.com/acme/authpkg",
		"name":    "authpkg",
		"runtime": "typescript-sandbox",
		"credentials": []map[string]any{{
			"name":     "workspace",
			"type":     "oauth2",
			"provider": "google",
			"scopes":   []string{"openid", "email"},
			"inject": map[string]any{
				"hosts":  []string{"www.googleapis.com"},
				"method": "bearer_header",
			},
		}},
		"tools": []map[string]any{{
			"entry_ts":   "tools/ping.ts",
			"effect":     "readOnly",
			"idempotent": true,
		}},
	}
	if opts.extraOAuthCredential {
		manifest["credentials"] = []map[string]any{{
			"name":     "workspace",
			"type":     "oauth2",
			"provider": "google",
			"scopes":   []string{"openid"},
			"inject": map[string]any{
				"hosts":  []string{"www.googleapis.com"},
				"method": "bearer_header",
			},
		}, {
			"name":     "workspace-admin",
			"type":     "oauth2",
			"provider": "google",
			"scopes":   []string{"openid", "email"},
			"inject": map[string]any{
				"hosts":  []string{"www.googleapis.com"},
				"method": "bearer_header",
			},
		}}
	}
	writeAuthJSONFile(t, filepath.Join(dir, "toolbox.devpkg.json"), manifest)
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "ping.ts"), []byte("export default async function ping() { return 'pong'; }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(ping.ts): %v", err)
	}
	return dir
}

func writeAuthJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeAuthIdentityFile(t *testing.T, dir string) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity(): %v", err)
	}
	path := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(path, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

type stubSecretStore struct{}

func (s *stubSecretStore) Get(context.Context, string) ([]byte, error) {
	return nil, secrets.ErrNotFound
}
func (s *stubSecretStore) Set(context.Context, string, []byte) error { return nil }
func (s *stubSecretStore) Delete(context.Context, string) error      { return secrets.ErrNotFound }
func (s *stubSecretStore) List(context.Context, string) ([]string, error) {
	return nil, nil
}

var _ io.Reader = (*bytes.Buffer)(nil)
