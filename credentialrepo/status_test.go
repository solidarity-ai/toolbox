package credentialrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestRenameAccountRejectsSameNameWithoutMutatingSecrets(t *testing.T) {
	t.Parallel()

	repo := testutil.NewTestCredentialRepo()
	pkg := tooldef.Package{
		Module: tooldef.ModulePath("fixtures.local/test-package"),
		Name:   "test-package",
		Credentials: []tooldef.PackageCredential{
			{Name: "weather", Type: "api_key"},
		},
	}

	workKey := credentialrepo.APIKeyRef(pkg, "weather", "work").String()
	repo.Seed(map[string][]byte{
		workKey: []byte("weather-work-key"),
	})

	_, err := repo.RenameAccount(context.Background(), pkg, "", "work", "work")
	if err == nil {
		t.Fatal("expected rename collision error, got nil")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error = %q, want same-name rejection", err.Error())
	}

	requireSecretValue(t, repo, credentialrepo.APIKeyRef(pkg, "weather", "work"), "weather-work-key")
	requireSecretMissing(t, repo, credentialrepo.APIKeyRef(pkg, "weather", "personal"))
}

func TestRenameAccountRejectsDestinationCollisionBeforeMovingAnySecrets(t *testing.T) {
	t.Parallel()

	repo := testutil.NewTestCredentialRepo()
	pkg := tooldef.Package{
		Module: tooldef.ModulePath("fixtures.local/test-package"),
		Name:   "test-package",
		Credentials: []tooldef.PackageCredential{
			{Name: "weather", Type: "api_key"},
			{Name: "payments", Type: "api_key"},
		},
	}

	repo.Seed(map[string][]byte{
		credentialrepo.APIKeyRef(pkg, "weather", "work").String():      []byte("weather-work-key"),
		credentialrepo.APIKeyRef(pkg, "payments", "work").String():     []byte("payments-work-key"),
		credentialrepo.APIKeyRef(pkg, "payments", "personal").String(): []byte("payments-personal-key"),
	})

	_, err := repo.RenameAccount(context.Background(), pkg, "", "work", "personal")
	if err == nil {
		t.Fatal("expected rename collision error, got nil")
	}
	if !strings.Contains(err.Error(), `account "personal" already exists for credential payments`) {
		t.Fatalf("error = %q, want destination collision", err.Error())
	}

	requireSecretValue(t, repo, credentialrepo.APIKeyRef(pkg, "weather", "work"), "weather-work-key")
	requireSecretMissing(t, repo, credentialrepo.APIKeyRef(pkg, "weather", "personal"))
	requireSecretValue(t, repo, credentialrepo.APIKeyRef(pkg, "payments", "work"), "payments-work-key")
	requireSecretValue(t, repo, credentialrepo.APIKeyRef(pkg, "payments", "personal"), "payments-personal-key")
}

func requireSecretValue(t *testing.T, repo *testutil.TestCredentialRepo, ref credentialrepo.Ref, want string) {
	t.Helper()

	got, err := repo.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get(%q) error: %v", ref, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", ref, string(got), want)
	}
}

func requireSecretMissing(t *testing.T, repo *testutil.TestCredentialRepo, ref credentialrepo.Ref) {
	t.Helper()

	_, err := repo.Get(context.Background(), ref)
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get(%q) error = %v, want %v", ref, err, secrets.ErrNotFound)
	}
}
