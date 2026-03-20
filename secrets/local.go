package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"filippo.io/age"
)

// LocalSecretStore is an age-encrypted, file-backed secret store.
// The store file is decrypted on first access and held in memory for the
// session lifetime. Mutations re-encrypt and flush to disk atomically.
type LocalSecretStore struct {
	storePath    string
	identityPath string

	once      sync.Once
	unlockErr error

	mu         sync.RWMutex
	data       map[string][]byte
	identities []age.Identity
	recipient  age.Recipient
}

// NewLocalSecretStore creates a new LocalSecretStore.
//
// storePath is the path to the age-encrypted store file. If empty, it defaults
// to $XDG_CONFIG_HOME/toolbox/secrets (or ~/.config/toolbox/secrets).
//
// identityPath is the path to the age identity (private key) file. If empty,
// it defaults to $XDG_CONFIG_HOME/age/keys.txt (or ~/.config/age/keys.txt).
//
// No I/O is performed until the first method call.
func NewLocalSecretStore(storePath string, identityPath string) *LocalSecretStore {
	if storePath == "" {
		storePath = defaultStorePath()
	}
	if identityPath == "" {
		identityPath = defaultIdentityPath()
	}
	return &LocalSecretStore{
		storePath:    storePath,
		identityPath: identityPath,
	}
}

func (s *LocalSecretStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := s.ensureUnlocked(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(val))
	copy(out, val)
	return out, nil
}

func (s *LocalSecretStore) Set(ctx context.Context, key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := s.ensureUnlocked(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := make([]byte, len(value))
	copy(stored, value)
	s.data[key] = stored
	return s.flush()
}

func (s *LocalSecretStore) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := s.ensureUnlocked(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		return ErrNotFound
	}
	delete(s.data, key)
	return s.flush()
}

func (s *LocalSecretStore) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.ensureUnlocked(); err != nil {
		return nil, err
	}
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

func (s *LocalSecretStore) ensureUnlocked() error {
	s.once.Do(func() {
		s.unlockErr = s.unlock()
	})
	return s.unlockErr
}

func (s *LocalSecretStore) unlock() error {
	identityFile, err := os.Open(s.identityPath)
	if err != nil {
		return fmt.Errorf("opening identity file: %w", err)
	}
	defer identityFile.Close()

	identities, err := age.ParseIdentities(identityFile)
	if err != nil {
		return fmt.Errorf("parsing identities: %w", err)
	}
	if len(identities) == 0 {
		return fmt.Errorf("no identities found in %s", s.identityPath)
	}

	// Extract recipient from the first identity for re-encryption.
	recipient, err := recipientFromIdentity(identities[0])
	if err != nil {
		return err
	}

	s.identities = identities
	s.recipient = recipient

	storeData, err := os.ReadFile(s.storePath)
	if os.IsNotExist(err) {
		s.data = make(map[string][]byte)
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading store file: %w", err)
	}

	decrypted, err := age.Decrypt(bytes.NewReader(storeData), s.identities...)
	if err != nil {
		return fmt.Errorf("decrypting store: %w", err)
	}

	plaintext, err := io.ReadAll(decrypted)
	if err != nil {
		return fmt.Errorf("reading decrypted store: %w", err)
	}

	data := make(map[string][]byte)
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return fmt.Errorf("parsing store data: %w", err)
	}
	s.data = data
	return nil
}

func (s *LocalSecretStore) flush() error {
	plaintext, err := json.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("marshaling store data: %w", err)
	}

	dir := filepath.Dir(s.storePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating store directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		// Clean up temp file on any error path.
		os.Remove(tmpPath)
	}()

	encWriter, err := age.Encrypt(tmpFile, s.recipient)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("initializing encryption: %w", err)
	}

	if _, err := encWriter.Write(plaintext); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing encrypted data: %w", err)
	}

	if err := encWriter.Close(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("finalizing encryption: %w", err)
	}

	if err := tmpFile.Chmod(0600); err != nil {
		tmpFile.Close()
		return fmt.Errorf("setting file permissions: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.storePath); err != nil {
		return fmt.Errorf("replacing store file: %w", err)
	}

	return nil
}

// recipientFromIdentity extracts the age.Recipient from a parsed identity.
// This works for X25519 identities which expose a Recipient() method.
func recipientFromIdentity(id age.Identity) (age.Recipient, error) {
	type recipientProvider interface {
		Recipient() *age.X25519Recipient
	}
	if rp, ok := id.(recipientProvider); ok {
		return rp.Recipient(), nil
	}
	return nil, fmt.Errorf("identity type %T does not expose a recipient", id)
}

func defaultStorePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "toolbox", "secrets")
}

func defaultIdentityPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "age", "keys.txt")
}
