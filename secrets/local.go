package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

const (
	scryptWorkFactorEnv    = "TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR"
	scryptMaxWorkFactorEnv = "TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR"
)

// LocalSecretStore is an age-encrypted, file-backed secret store.
// The store file is decrypted on first access and held in memory for the
// session lifetime. Mutations re-encrypt and flush to disk atomically.
type LocalSecretStore struct {
	storePath        string
	identityPath     string
	defaultUnlockKey string
	backupCodeWriter io.Writer

	unlockMu              sync.Mutex
	mu                    sync.RWMutex
	data                  map[string][]byte
	identities            []age.Identity
	identityText          []byte
	recipient             age.Recipient
	unlocked              bool
	unlockedWithRecovery  bool
	recoveryUnlockedUntil time.Time
}

// NewLocalSecretStore creates a new LocalSecretStore.
//
// storePath is the path to the age-encrypted store file. If empty, it defaults
// to $XDG_CONFIG_HOME/toolbox/secrets (or ~/.config/toolbox/secrets).
//
// identityPath is the path to the wrapped age identity file. If empty, it
// defaults to $XDG_CONFIG_HOME/toolbox/keys.txt.age (or ~/.config/toolbox/keys.txt.age).
//
// No I/O is performed until the first method call or Unlock call.
func NewLocalSecretStore(storePath string, identityPath string) *LocalSecretStore {
	return NewLocalSecretStoreWithKey(storePath, identityPath, "")
}

// NewLocalSecretStoreWithKey creates a LocalSecretStore that can auto-unlock
// using the provided secret key on first access.
func NewLocalSecretStoreWithKey(storePath string, identityPath string, unlockKey string) *LocalSecretStore {
	if storePath == "" {
		storePath = defaultStorePath()
	}
	return &LocalSecretStore{
		storePath:        storePath,
		identityPath:     resolveIdentityPath(identityPath),
		defaultUnlockKey: unlockKey,
	}
}

// SetBackupCodeWriter configures where backup codes generated during automatic
// first-time setup are written. Explicit Setup callers receive codes directly.
func (s *LocalSecretStore) SetBackupCodeWriter(w io.Writer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backupCodeWriter = w
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

func (s *LocalSecretStore) Unlock(ctx context.Context, unlockKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(unlockKey) == "" {
		return ErrLocked
	}

	s.unlockMu.Lock()
	defer s.unlockMu.Unlock()

	s.mu.RLock()
	if s.unlocked {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	state, err := s.loadUnlockedState(ctx, unlockKey)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = state.data
	s.identities = state.identities
	s.identityText = state.identityText
	s.recipient = state.recipient
	s.unlocked = true
	s.unlockedWithRecovery = state.unlockedWithRecovery
	s.recoveryUnlockedUntil = state.recoveryUnlockedUntil
	return nil
}

func (s *LocalSecretStore) Lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = nil
	s.identities = nil
	s.identityText = nil
	s.recipient = nil
	s.unlocked = false
	s.unlockedWithRecovery = false
	s.recoveryUnlockedUntil = time.Time{}
	return nil
}

func (s *LocalSecretStore) ensureUnlocked() error {
	s.mu.RLock()
	unlocked := s.unlocked
	s.mu.RUnlock()
	if unlocked {
		return nil
	}
	if strings.TrimSpace(s.defaultUnlockKey) == "" {
		return ErrLocked
	}
	err := s.Unlock(context.Background(), s.defaultUnlockKey)
	if errors.Is(err, ErrNotInitialized) {
		if s.getBackupCodeWriter() == nil {
			return ErrNotInitialized
		}
		codes, setupErr := s.Setup(context.Background(), s.defaultUnlockKey)
		if setupErr == nil {
			s.writeBackupCodes(codes)
		}
		return setupErr
	}
	return err
}

func (s *LocalSecretStore) flush() error {
	return writeEncryptedStore(s.storePath, s.recipient, s.data)
}

func (s *LocalSecretStore) getBackupCodeWriter() io.Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backupCodeWriter
}

type unlockedState struct {
	data                  map[string][]byte
	identities            []age.Identity
	identityText          []byte
	recipient             age.Recipient
	unlockedWithRecovery  bool
	recoveryUnlockedUntil time.Time
}

func (s *LocalSecretStore) loadUnlockedState(ctx context.Context, unlockKey string) (unlockedState, error) {
	state, err := s.loadWrappedIdentity(unlockKey)
	if err == nil {
		state.data, err = readStoreData(s.storePath, state.identities)
		if err != nil {
			return unlockedState{}, err
		}
		return state, nil
	}

	storeExists, existsErr := fileExists(s.storePath)
	if existsErr != nil {
		return unlockedState{}, existsErr
	}
	if storeExists {
		if recoveryState, recoveryErr := loadBackupCodeIdentity(unlockKey); recoveryErr == nil {
			recoveryState.data, err = readStoreData(s.storePath, recoveryState.identities)
			if err != nil {
				return unlockedState{}, err
			}
			recoveryState.unlockedWithRecovery = true
			recoveryState.recoveryUnlockedUntil = timeNow().Add(5 * time.Minute)
			return recoveryState, nil
		}
	}

	if errors.Is(err, os.ErrNotExist) {
		if storeExists {
			return unlockedState{}, fmt.Errorf("opening wrapped identity file: %w", os.ErrNotExist)
		}
		return unlockedState{}, ErrNotInitialized
	}
	return unlockedState{}, err
}

func (s *LocalSecretStore) loadWrappedIdentity(unlockKey string) (unlockedState, error) {
	wrappedBytes, err := os.ReadFile(s.identityPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return unlockedState{}, os.ErrNotExist
		}
		return unlockedState{}, fmt.Errorf("opening wrapped identity file: %w", err)
	}

	identity, err := newScryptIdentity(unlockKey)
	if err != nil {
		return unlockedState{}, err
	}
	decrypted, err := age.Decrypt(bytes.NewReader(wrappedBytes), identity)
	if err != nil {
		return unlockedState{}, fmt.Errorf("decrypting wrapped identity: %w", err)
	}

	identityText, err := io.ReadAll(decrypted)
	if err != nil {
		return unlockedState{}, fmt.Errorf("reading wrapped identity: %w", err)
	}

	return parseIdentityText(identityText)
}

func (s *LocalSecretStore) createWrappedIdentity(ctx context.Context, unlockKey string) (unlockedState, error) {
	_, _ = ctx, unlockKey
	identityText, state, err := generateWrappedIdentity()
	if err != nil {
		return unlockedState{}, err
	}
	if err := writeWrappedIdentityFile(s.identityPath, unlockKey, identityText); err != nil {
		return unlockedState{}, err
	}
	return state, nil
}

func readStoreData(storePath string, identities []age.Identity) (map[string][]byte, error) {
	storeData, err := os.ReadFile(storePath)
	if os.IsNotExist(err) {
		return make(map[string][]byte), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading store file: %w", err)
	}

	decrypted, err := age.Decrypt(bytes.NewReader(storeData), identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypting store: %w", err)
	}

	plaintext, err := io.ReadAll(decrypted)
	if err != nil {
		return nil, fmt.Errorf("reading decrypted store: %w", err)
	}

	data := make(map[string][]byte)
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("parsing store data: %w", err)
	}
	return data, nil
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

func generateWrappedIdentity() ([]byte, unlockedState, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, unlockedState{}, fmt.Errorf("generating age key: %w", err)
	}
	identityText := []byte(fmt.Sprintf("# created by toolbox\n# public key: %s\n%s\n", identity.Recipient(), identity))

	state, err := parseIdentityText(identityText)
	if err != nil {
		return nil, unlockedState{}, err
	}
	return identityText, state, nil
}

func parseIdentityText(identityText []byte) (unlockedState, error) {
	identities, err := age.ParseIdentities(bytes.NewReader(identityText))
	if err != nil {
		return unlockedState{}, fmt.Errorf("parsing identities: %w", err)
	}
	if len(identities) == 0 {
		return unlockedState{}, fmt.Errorf("no identities found")
	}

	recipient, err := recipientFromIdentity(identities[0])
	if err != nil {
		return unlockedState{}, err
	}
	return unlockedState{
		identities:   identities,
		identityText: append([]byte(nil), identityText...),
		recipient:    recipient,
	}, nil
}

func writeWrappedIdentityFile(path string, unlockKey string, identityText []byte) error {
	recipient, err := newScryptRecipient(unlockKey)
	if err != nil {
		return fmt.Errorf("creating wrapped identity recipient: %w", err)
	}
	return writeEncryptedFile(path, recipient, identityText)
}

func writeEncryptedStore(storePath string, recipient age.Recipient, data map[string][]byte) error {
	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling store data: %w", err)
	}
	return writeEncryptedFile(storePath, recipient, plaintext)
}

func writeEncryptedFile(path string, recipient age.Recipient, plaintext []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	encWriter, err := age.Encrypt(tmpFile, recipient)
	if err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("initializing encryption: %w", err)
	}

	if _, err := encWriter.Write(plaintext); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing encrypted data: %w", err)
	}

	if err := encWriter.Close(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("finalizing encryption: %w", err)
	}

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("setting file permissions: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replacing output file: %w", err)
	}

	return nil
}

func newScryptRecipient(unlockKey string) (*age.ScryptRecipient, error) {
	recipient, err := age.NewScryptRecipient(unlockKey)
	if err != nil {
		return nil, err
	}
	if workFactor, ok, err := scryptEnvInt(scryptWorkFactorEnv); err != nil {
		return nil, err
	} else if ok {
		recipient.SetWorkFactor(workFactor)
	}
	return recipient, nil
}

func newScryptIdentity(unlockKey string) (*age.ScryptIdentity, error) {
	identity, err := age.NewScryptIdentity(unlockKey)
	if err != nil {
		return nil, err
	}
	if maxWorkFactor, ok, err := scryptEnvInt(scryptMaxWorkFactorEnv); err != nil {
		return nil, err
	} else if ok {
		identity.SetMaxWorkFactor(maxWorkFactor)
	}
	return identity, nil
}

func scryptEnvInt(name string) (int, bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, true, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func resolveIdentityPath(identityPath string) string {
	if strings.TrimSpace(identityPath) == "" {
		return defaultWrappedIdentityPath()
	}
	identityPath = filepath.Clean(identityPath)
	if strings.HasSuffix(identityPath, ".age") {
		return identityPath
	}
	return identityPath + ".age"
}

func defaultStorePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "toolbox", "secrets")
}

func defaultWrappedIdentityPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "toolbox", "keys.txt.age")
}
