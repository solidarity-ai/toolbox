package testutil

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/solidarity-ai/toolbox/secrets"
)

// TestSecretStore is a plaintext in-memory SecretStore for use in tests.
type TestSecretStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewTestSecretStore creates a new empty TestSecretStore.
func NewTestSecretStore() *TestSecretStore {
	return &TestSecretStore{
		data: make(map[string][]byte),
	}
}

// Seed prepopulates the store with the given entries.
func (s *TestSecretStore) Seed(entries map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range entries {
		stored := make([]byte, len(v))
		copy(stored, v)
		s.data[k] = stored
	}
}

// SeedStrings prepopulates the store from string values without exposing test
// code to repeated []byte conversions.
func (s *TestSecretStore) SeedStrings(entries map[string]string) {
	converted := make(map[string][]byte, len(entries))
	for k, v := range entries {
		converted[k] = []byte(v)
	}
	s.Seed(converted)
}

func (s *TestSecretStore) Get(_ context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, secrets.ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

func (s *TestSecretStore) Set(_ context.Context, key string, value []byte) error {
	if key == "" {
		return secrets.ErrInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := make([]byte, len(value))
	copy(stored, value)
	s.data[key] = stored
	return nil
}

func (s *TestSecretStore) Delete(_ context.Context, key string) error {
	if key == "" {
		return secrets.ErrInvalidKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		return secrets.ErrNotFound
	}
	delete(s.data, key)
	return nil
}

func (s *TestSecretStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var keys []string
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
