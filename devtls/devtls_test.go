package devtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/secrets"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string][]byte
	sets   int
}

type unavailableSecrets struct{}

func (unavailableSecrets) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("secret store is locked")
}
func (unavailableSecrets) Set(context.Context, string, []byte) error {
	return errors.New("secret store is locked")
}
func (unavailableSecrets) Delete(context.Context, string) error {
	return errors.New("secret store is locked")
}
func (unavailableSecrets) List(context.Context, string) ([]string, error) {
	return nil, errors.New("secret store is locked")
}

func newMemorySecrets() *memorySecrets { return &memorySecrets{values: map[string][]byte{}} }
func (s *memorySecrets) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return cloneBytes(v), nil
}
func (s *memorySecrets) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = cloneBytes(value)
	s.sets++
	return nil
}
func (s *memorySecrets) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; !ok {
		return secrets.ErrNotFound
	}
	delete(s.values, key)
	return nil
}
func (s *memorySecrets) List(_ context.Context, _ string) ([]string, error) { return nil, nil }

type fakeTrust struct {
	trusted    map[string]bool
	installs   int
	removes    int
	installErr error
	removeErr  error
}

func newFakeTrust() *fakeTrust { return &fakeTrust{trusted: map[string]bool{}} }
func (s *fakeTrust) Trusted(_ context.Context, root, _ []byte, _ string) (bool, error) {
	cert, err := parseCertificate(root)
	if err != nil {
		return false, err
	}
	return s.trusted[fingerprint(cert)], nil
}
func (s *fakeTrust) Install(_ context.Context, root []byte) error {
	if s.installErr != nil {
		return s.installErr
	}
	if strings.Contains(string(root), "PRIVATE KEY") {
		return errors.New("private key crossed trust boundary")
	}
	cert, err := parseCertificate(root)
	if err != nil {
		return err
	}
	s.trusted[fingerprint(cert)] = true
	s.installs++
	return nil
}
func (s *fakeTrust) Remove(_ context.Context, root []byte) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	cert, err := parseCertificate(root)
	if err != nil {
		return err
	}
	delete(s.trusted, fingerprint(cert))
	s.removes++
	return nil
}

func TestGenerateReturnsSerializableUsableBundleWithoutPersistence(t *testing.T) {
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.TLSCertificate(); err != nil {
		t.Fatalf("TLSCertificate: %v", err)
	}
	encoded, err := bundle.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseBundle(encoded)
	if err != nil {
		t.Fatal(err)
	}
	material, err := materialFromBundle(decoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := material.leafCert.VerifyHostname(host); err != nil {
			t.Errorf("SAN %s: %v", host, err)
		}
	}
}

func TestNativeCommandTemporaryFilesContainOnlyPublicCertificates(t *testing.T) {
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	rootPath, leafPath, cleanup, err := writePublicCerts(bundle.RootCertPEM, bundle.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{rootPath, leafPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "PRIVATE KEY") {
			t.Fatalf("private key found in %s", path)
		}
	}
	cleanup()
	for _, path := range []string{rootPath, leafPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary certificate remains at %s: %v", path, err)
		}
	}
}

func TestInstallPersistsOneSecretAndIsIdempotent(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	first, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.Status.Usable {
		t.Fatalf("first: %+v", first)
	}
	if trust.installs != 1 {
		t.Fatalf("installs=%d", trust.installs)
	}
	if len(secretStore.values) != 1 {
		t.Fatalf("secret count=%d", len(secretStore.values))
	}
	if !strings.Contains(string(secretStore.values["toolbox/devtls"]), "root_key_pem") {
		t.Fatal("secret does not contain bundle")
	}
	identity, err := os.ReadFile(m.cfg.IdentityPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(identity), string(first.Bundle.RootKeyPEM)) || strings.Contains(string(identity), string(first.Bundle.RootCertPEM)) {
		t.Fatal("serving identity contains CA material")
	}
	if !strings.Contains(string(identity), "PRIVATE KEY") {
		t.Fatal("serving identity does not contain leaf key")
	}
	if info, err := os.Stat(m.cfg.IdentityPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions=%#o, want 0600", info.Mode().Perm())
	}
	second, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || !second.Status.Usable {
		t.Fatalf("second: %+v", second)
	}
	if trust.installs != 1 {
		t.Fatalf("repeat installs=%d", trust.installs)
	}
}

func TestSharedManagerSerializesConcurrentInstall(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := m.Install(context.Background())
			if err == nil && !result.Status.Usable {
				err = errors.New("installed certificate is not usable")
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if trust.installs != 1 {
		t.Fatalf("native installs=%d, want 1", trust.installs)
	}
}

func TestManagerTLSConfigLoadsIdentityWhileSecretStoreIsLocked(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	m.cfg.Secrets = unavailableSecrets{}
	config, err := m.TLSConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS12 || len(config.Certificates) != 1 {
		t.Fatalf("TLS config=%+v", config)
	}
	if trust.installs != 0 || trust.removes != 0 {
		t.Fatalf("TLSConfig changed trust: installs=%d removes=%d", trust.installs, trust.removes)
	}
}

func TestInstallRepairsMissingServingIdentity(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	first, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.cfg.IdentityPath); err != nil {
		t.Fatal(err)
	}
	status, err := m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Usable || len(status.Problems) == 0 {
		t.Fatalf("missing identity status=%+v", status)
	}
	result, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Status.Usable {
		t.Fatalf("repair result=%+v", result)
	}
	certificate, err := LoadTLSCertificate(m.cfg.IdentityPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(certificate.Certificate) == 0 || certFingerprint(t, first.Bundle.CertPEM) != certDERFingerprint(certificate.Certificate[0]) {
		t.Fatal("repair did not restore the saved leaf")
	}
}

func TestLoadTLSCertificateRejectsLooseUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows identities are protected with DPAPI")
	}
	secretStore := newMemorySecrets()
	m := testManager(t, secretStore, newFakeTrust(), nil)
	if _, err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.cfg.IdentityPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTLSCertificate(m.cfg.IdentityPath); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("LoadTLSCertificate error=%v", err)
	}
}

func TestSaveImportsGeneratedBundleWithoutChangingTrust(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	bundle, err := Generate(Config{})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := certFingerprint(t, bundle.RootCertPEM)
	if err := m.Save(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	if trust.installs != 0 {
		t.Fatal("Save changed native trust")
	}
	result, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.RootFingerprint != wantRoot || trust.installs != 1 {
		t.Fatalf("import was not reused: %+v installs=%d", result, trust.installs)
	}
}

func TestInstallRenewsLeafInSecretWithoutRotatingCA(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := testManager(t, secretStore, trust, &now)
	first, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldLeaf := certFingerprint(t, first.Bundle.CertPEM)
	now = now.Add(80 * 24 * time.Hour)
	result, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Rotated || result.Status.RootFingerprint != first.Status.RootFingerprint {
		t.Fatalf("renewal rotated CA: %+v", result)
	}
	if certFingerprint(t, result.Bundle.CertPEM) == oldLeaf {
		t.Fatal("leaf was not renewed")
	}
	if trust.installs != 1 || trust.removes != 0 {
		t.Fatalf("trust changed: %d/%d", trust.installs, trust.removes)
	}
}

func TestRotationJournalsAndRetriesOldTrustCleanupInSecret(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	first, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	trust.removeErr = errors.New("authorization cancelled")
	result, err := m.Rotate(context.Background())
	if err == nil {
		t.Fatal("Rotate succeeded")
	}
	if result.Status.RootFingerprint == first.Status.RootFingerprint {
		t.Fatal("new CA was not committed")
	}
	var state storedState
	if err := json.Unmarshal(secretStore.values["toolbox/devtls"], &state); err != nil {
		t.Fatal(err)
	}
	if len(state.StaleRoots) != 1 {
		t.Fatalf("stale roots=%d", len(state.StaleRoots))
	}
	trust.removeErr = nil
	result, err = m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Status.Usable || len(trust.trusted) != 1 {
		t.Fatalf("cleanup failed: %+v trusted=%v", result, trust.trusted)
	}
}

func TestFailedTrustInstallLeavesBundleSavedForRetry(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	trust.installErr = errors.New("denied")
	m := testManager(t, secretStore, trust, nil)
	if _, err := m.Install(context.Background()); err == nil {
		t.Fatal("Install succeeded")
	}
	before := cloneBytes(secretStore.values["toolbox/devtls"])
	trust.installErr = nil
	result, err := m.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Status.Usable {
		t.Fatalf("retry: %+v", result)
	}
	if string(before) != string(secretStore.values["toolbox/devtls"]) {
		t.Fatal("retry regenerated saved identity")
	}
}

func TestRemoveDeletesTrustThenSecret(t *testing.T) {
	secretStore := newMemorySecrets()
	trust := newFakeTrust()
	m := testManager(t, secretStore, trust, nil)
	if _, err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	trust.removeErr = errors.New("cancelled")
	if err := m.Remove(context.Background()); err == nil {
		t.Fatal("Remove succeeded")
	}
	if len(secretStore.values) != 1 {
		t.Fatal("secret deleted despite trust failure")
	}
	trust.removeErr = nil
	if err := m.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(secretStore.values) != 0 {
		t.Fatal("secret remains")
	}
	if _, err := os.Stat(m.cfg.IdentityPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("serving identity remains: %v", err)
	}
	if err := m.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func testManager(t *testing.T, secretStore secrets.SecretStore, trust TrustStore, now *time.Time) *Manager {
	t.Helper()
	cfg := Config{
		Secrets:      secretStore,
		SecretKey:    "toolbox/devtls",
		IdentityPath: filepath.Join(t.TempDir(), "identity.pem"),
		Trust:        trust,
	}
	if now != nil {
		cfg.now = func() time.Time { return *now }
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func certFingerprint(t *testing.T, pem []byte) string {
	t.Helper()
	cert, err := parseCertificate(pem)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint(cert)
}

func certDERFingerprint(der []byte) string {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return ""
	}
	return fingerprint(cert)
}
