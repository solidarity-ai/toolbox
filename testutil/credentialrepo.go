package testutil

import "github.com/solidarity-ai/toolbox/credentialrepo"

// TestCredentialRepo is a credentialrepo.Repository backed by an in-memory
// TestSecretStore. It exposes Seed for convenient test setup.
type TestCredentialRepo struct {
	*credentialrepo.Repository
	Store *TestSecretStore
}

func NewTestCredentialRepo() *TestCredentialRepo {
	store := NewTestSecretStore()
	return &TestCredentialRepo{
		Repository: credentialrepo.New(store),
		Store:      store,
	}
}

func (r *TestCredentialRepo) Seed(entries map[string][]byte) {
	r.Store.Seed(entries)
}
