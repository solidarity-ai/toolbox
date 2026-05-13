package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRecoveryCodeCount = 3
	backupCodeVersionPrefix  = "TBX1"
	backupCodeNonceBytes     = 16
)

func (s *LocalSecretStore) Initialized() (bool, error) {
	identityExists, err := fileExists(s.identityPath)
	if err != nil {
		return false, err
	}
	if identityExists {
		return true, nil
	}
	storeExists, err := fileExists(s.storePath)
	if err != nil {
		return false, err
	}
	return storeExists, nil
}

func (s *LocalSecretStore) Setup(ctx context.Context, unlockKey string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(unlockKey) == "" {
		return nil, ErrLocked
	}
	s.unlockMu.Lock()
	defer s.unlockMu.Unlock()
	initialized, err := s.Initialized()
	if err != nil {
		return nil, err
	}
	if initialized {
		return nil, fmt.Errorf("secret store already initialized")
	}
	identityText, state, err := generateWrappedIdentity()
	if err != nil {
		return nil, err
	}
	codes, err := newBackupCodes(identityText, defaultRecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	state.data = make(map[string][]byte)

	wroteIdentity := false
	wroteStore := false
	rollback := func() {
		if wroteStore {
			_ = os.Remove(s.storePath)
		}
		if wroteIdentity {
			_ = os.Remove(s.identityPath)
		}
	}
	if err := writeWrappedIdentityFile(s.identityPath, unlockKey, identityText); err != nil {
		return nil, err
	}
	wroteIdentity = true
	if err := writeEncryptedStore(s.storePath, state.recipient, state.data); err != nil {
		rollback()
		return nil, err
	}
	wroteStore = true

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = state.data
	s.identities = state.identities
	s.identityText = state.identityText
	s.recipient = state.recipient
	s.unlocked = true
	s.unlockedWithRecovery = false
	s.recoveryUnlockedUntil = timeZero()
	return codes, nil
}

func (s *LocalSecretStore) BackupCodes(ctx context.Context, unlockKey string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := s.loadWrappedIdentity(unlockKey)
	if err != nil {
		return nil, err
	}
	return newBackupCodes(state.identityText, defaultRecoveryCodeCount)
}

func (s *LocalSecretStore) RecoveryCodes(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureUnlocked(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	identityText := append([]byte(nil), s.identityText...)
	s.mu.RUnlock()
	if len(identityText) == 0 {
		return nil, ErrLocked
	}
	return newBackupCodes(identityText, defaultRecoveryCodeCount)
}

func (s *LocalSecretStore) RecoveryUnlocked(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unlocked && s.unlockedWithRecovery, nil
}

func (s *LocalSecretStore) RewrapAfterRecovery(ctx context.Context, newUnlockKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(newUnlockKey) == "" {
		return ErrLocked
	}
	s.mu.RLock()
	unlocked := s.unlocked
	unlockedWithRecovery := s.unlockedWithRecovery
	recoveryUnlockedUntil := s.recoveryUnlockedUntil
	identityText := append([]byte(nil), s.identityText...)
	s.mu.RUnlock()
	ok := unlocked && unlockedWithRecovery && timeNow().Before(recoveryUnlockedUntil)
	if !ok {
		if unlockedWithRecovery && !recoveryUnlockedUntil.IsZero() && !timeNow().Before(recoveryUnlockedUntil) {
			return ErrRecoveryWindowExpired
		}
		return ErrRecoveryRequired
	}
	if len(identityText) == 0 {
		return ErrLocked
	}
	if err := writeWrappedIdentityFile(s.identityPath, newUnlockKey, identityText); err != nil {
		return err
	}
	s.mu.Lock()
	s.recoveryUnlockedUntil = timeZero()
	s.mu.Unlock()
	return nil
}

func loadBackupCodeIdentity(code string) (unlockedState, error) {
	identityText, err := decodeBackupCode(code)
	if err != nil {
		return unlockedState{}, err
	}
	return parseIdentityText(identityText)
}

func newBackupCodes(identityText []byte, count int) ([]string, error) {
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code, err := encodeBackupCode(identityText)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func encodeBackupCode(identityText []byte) (string, error) {
	nonce, err := randomBytes(backupCodeNonceBytes)
	if err != nil {
		return "", err
	}
	payload := make([]byte, 0, len(nonce)+len(identityText))
	payload = append(payload, nonce...)
	payload = append(payload, identityText...)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(payload)
	return backupCodeVersionPrefix + "-" + strings.Join(splitRecoveryCode(encoded, 8), "-"), nil
}

func decodeBackupCode(code string) ([]byte, error) {
	normalizedCode := normalizeRecoveryCode(code)
	if !strings.HasPrefix(normalizedCode, backupCodeVersionPrefix) {
		return nil, errors.New("invalid backup code")
	}
	encoded := strings.TrimPrefix(normalizedCode, backupCodeVersionPrefix)
	payload, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid backup code")
	}
	if len(payload) <= backupCodeNonceBytes {
		return nil, errors.New("invalid backup code")
	}
	return append([]byte(nil), payload[backupCodeNonceBytes:]...), nil
}

func randomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := rand.Read(buf)
	return buf, err
}

func splitRecoveryCode(code string, groupSize int) []string {
	groups := make([]string, 0, (len(code)+groupSize-1)/groupSize)
	for len(code) > groupSize {
		groups = append(groups, code[:groupSize])
		code = code[groupSize:]
	}
	if code != "" {
		groups = append(groups, code)
	}
	return groups
}

func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

func WriteBackupCodes(w io.Writer, codes []string) {
	if w == nil || len(codes) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "Toolbox secret store backup codes:")
	_, _ = fmt.Fprintln(w, "Save these codes in your password manager. Toolbox does not store them and cannot show them again.")
	for _, code := range codes {
		_, _ = fmt.Fprintf(w, "  %s\n", code)
	}
}

func (s *LocalSecretStore) writeBackupCodes(codes []string) {
	s.mu.RLock()
	w := s.backupCodeWriter
	s.mu.RUnlock()
	WriteBackupCodes(w, codes)
}

var timeNow = time.Now

func timeZero() time.Time { return time.Time{} }
