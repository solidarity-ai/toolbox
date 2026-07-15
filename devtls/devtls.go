// Package devtls generates and manages a private development CA and localhost
// TLS certificate. The CA is persisted in a Toolbox secret store. A restricted
// serving identity is persisted separately so a daemon can start HTTPS while
// the secret store is locked.
package devtls

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/secrets"
)

const (
	defaultName  = "Toolbox Local Development CA"
	stateVersion = 1
)

var (
	ErrNotInstalled     = errors.New("development TLS certificate is not installed")
	ErrAlreadyInstalled = errors.New("development TLS certificate is already saved")
	ErrUnsupportedOS    = errors.New("development TLS trust store is unsupported on this OS")
	ErrUnsupportedScope = errors.New("requested trust scope is unsupported on this OS")
)

type Scope uint8

const (
	ScopeAuto Scope = iota
	ScopeUser
	ScopeSystem
)

type PrivilegeMode uint8

const (
	PrivilegePrompt PrivilegeMode = iota
	PrivilegeAlreadyElevated
)

// Config controls generation, secret persistence, the restricted serving
// identity, and native trust. Secrets, SecretKey, and IdentityPath are required
// by New, but not by Generate.
type Config struct {
	Name        string
	Hosts       []string
	Scope       Scope
	Privilege   PrivilegeMode
	CAValidity  time.Duration
	Validity    time.Duration
	RenewBefore time.Duration

	Secrets      secrets.SecretStore
	SecretKey    string
	IdentityPath string
	Trust        TrustStore

	now  func() time.Time
	rand io.Reader
}

// Bundle is the complete exportable certificate set. Callers may save it in a
// secret store, pass CertPEM/KeyPEM to a server, or use TLSCertificate.
type Bundle struct {
	RootCertPEM []byte `json:"root_cert_pem"`
	RootKeyPEM  []byte `json:"root_key_pem"`
	CertPEM     []byte `json:"cert_pem"`
	KeyPEM      []byte `json:"key_pem"`
}

func (b Bundle) TLSCertificate() (tls.Certificate, error) {
	return tls.X509KeyPair(b.CertPEM, b.KeyPEM)
}

// TLSConfig returns a fileless server configuration backed by this bundle.
func (b Bundle) TLSConfig() (*tls.Config, error) {
	certificate, err := b.TLSCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func (b Bundle) MarshalBinary() ([]byte, error) { return json.Marshal(b) }

func ParseBundle(data []byte) (Bundle, error) {
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode development TLS bundle: %w", err)
	}
	material, err := materialFromBundle(bundle)
	if err != nil {
		return Bundle{}, err
	}
	if err := validateBundleStructure(material); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

type Status struct {
	Installed       bool
	Trusted         bool
	Usable          bool
	NeedsRotation   bool
	RootFingerprint string
	LeafNotAfter    time.Time
	Problems        []string
}

type Result struct {
	Status  Status
	Bundle  Bundle
	Changed bool
	Rotated bool
}

// TrustStore is the native OS trust boundary. Implementations may write public
// certificates to short-lived temporary files for native commands. The byte
// slices include no private keys.
type TrustStore interface {
	Trusted(ctx context.Context, rootCertPEM, leafCertPEM []byte, hostname string) (bool, error)
	Install(ctx context.Context, rootCertPEM []byte) error
	Remove(ctx context.Context, rootCertPEM []byte) error
}

type Manager struct {
	cfg   Config
	trust TrustStore
	mu    sync.Mutex
}

type storedState struct {
	Version    int      `json:"version"`
	Current    Bundle   `json:"current"`
	Pending    *Bundle  `json:"pending,omitempty"`
	StaleRoots [][]byte `json:"stale_roots,omitempty"`
}

func New(cfg Config) (*Manager, error) {
	if cfg.Secrets == nil {
		return nil, errors.New("devtls: Secrets is required")
	}
	if strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("devtls: SecretKey is required")
	}
	if strings.TrimSpace(cfg.IdentityPath) == "" {
		return nil, errors.New("devtls: IdentityPath is required")
	}
	absIdentityPath, err := filepath.Abs(cfg.IdentityPath)
	if err != nil {
		return nil, fmt.Errorf("devtls: resolve IdentityPath: %w", err)
	}
	cfg.IdentityPath = absIdentityPath
	if err := applyDefaultsAndValidate(&cfg); err != nil {
		return nil, err
	}
	trust := cfg.Trust
	if trust == nil {
		var err error
		trust, err = newNativeStore(nativeStoreConfig{name: cfg.Name, scope: resolvedScope(cfg.Scope), privilege: cfg.Privilege})
		if err != nil {
			return nil, err
		}
	}
	return &Manager{cfg: cfg, trust: trust}, nil
}

// Generate creates a new bundle without saving it or changing native trust.
func Generate(cfg Config) (Bundle, error) {
	if err := applyDefaultsAndValidate(&cfg); err != nil {
		return Bundle{}, err
	}
	m, err := generateMaterial(cfg)
	if err != nil {
		return Bundle{}, err
	}
	return m.bundle(), nil
}

func (m *Manager) Bundle(ctx context.Context) (Bundle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.load(ctx)
	if err != nil {
		return Bundle{}, err
	}
	return cloneBundle(state.Current), nil
}

// TLSConfig loads only the restricted serving identity. It does not access the
// secret store, install trust, or invoke a native command, so a daemon can use
// it before the CA secret store has been unlocked.
func (m *Manager) TLSConfig(ctx context.Context) (*tls.Config, error) {
	_ = ctx
	return LoadTLSConfig(m.cfg.IdentityPath)
}

// TLSListener wraps a listener with the currently saved certificate. Rotation
// takes effect for a new listener (or after the caller reloads TLSConfig).
func (m *Manager) TLSListener(ctx context.Context, listener net.Listener) (net.Listener, error) {
	if listener == nil {
		return nil, errors.New("devtls: listener is nil")
	}
	config, err := m.TLSConfig(ctx)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(listener, config), nil
}

// Save validates and persists a generated or imported bundle without changing
// native trust. A later Install will check and provision trust idempotently.
func (m *Manager) Save(ctx context.Context, bundle Bundle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.load(ctx); err == nil {
		return ErrAlreadyInstalled
	} else if !errors.Is(err, ErrNotInstalled) {
		return err
	}
	material, err := materialFromBundle(bundle)
	if err != nil {
		return err
	}
	if err := validateMaterial(material, m.cfg); err != nil {
		return err
	}
	if err := writeServingIdentity(m.cfg.IdentityPath, bundle); err != nil {
		return err
	}
	return m.saveState(ctx, storedState{Version: stateVersion, Current: cloneBundle(bundle)})
}

func (m *Manager) Install(ctx context.Context) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installLocked(ctx)
}

func (m *Manager) installLocked(ctx context.Context) (Result, error) {
	state, err := m.load(ctx)
	changed := false
	if errors.Is(err, ErrNotInstalled) {
		bundle, genErr := Generate(m.cfg)
		if genErr != nil {
			return Result{}, genErr
		}
		if err := writeServingIdentity(m.cfg.IdentityPath, bundle); err != nil {
			return Result{}, err
		}
		state = storedState{Version: stateVersion, Current: bundle}
		if err := m.saveState(ctx, state); err != nil {
			return Result{}, err
		}
		changed = true
	} else if err != nil {
		return Result{}, err
	}

	if state.Pending != nil {
		state, err = m.finishPendingRotation(ctx, state)
		if err != nil {
			return Result{}, err
		}
		changed = true
	}
	if err := m.cleanupStale(ctx, &state); err != nil {
		return Result{}, err
	}

	material, err := materialFromBundle(state.Current)
	if err != nil {
		return m.rotateFrom(ctx, state)
	}
	if !rootCanSign(material, m.cfg.now()) || rootNeedsRotation(material, m.cfg) {
		return m.rotateFrom(ctx, state)
	}
	if err := validateMaterial(material, m.cfg); err != nil || leafNeedsRenewal(material, m.cfg.now(), m.cfg.RenewBefore) {
		material, err = renewLeaf(material, m.cfg)
		if err != nil {
			return Result{}, err
		}
		state.Current = material.bundle()
		if err := writeServingIdentity(m.cfg.IdentityPath, state.Current); err != nil {
			return Result{}, err
		}
		if err := m.saveState(ctx, state); err != nil {
			return Result{}, err
		}
		changed = true
	}
	identityCurrent, err := servingIdentityMatches(m.cfg.IdentityPath, state.Current)
	if err != nil || !identityCurrent {
		if err := writeServingIdentity(m.cfg.IdentityPath, state.Current); err != nil {
			return Result{}, err
		}
		changed = true
	}

	trusted, err := m.trust.Trusted(ctx, state.Current.RootCertPEM, state.Current.CertPEM, firstDNSName(m.cfg.Hosts))
	if err != nil {
		return Result{}, fmt.Errorf("check native trust: %w", err)
	}
	if !trusted {
		if err := m.trust.Install(ctx, state.Current.RootCertPEM); err != nil {
			return Result{}, fmt.Errorf("install native trust: %w", err)
		}
		changed = true
	}
	status, err := m.checkState(ctx, state)
	if err != nil {
		return Result{}, err
	}
	return Result{Status: status, Bundle: cloneBundle(state.Current), Changed: changed}, nil
}

func (m *Manager) Check(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.load(ctx)
	if errors.Is(err, ErrNotInstalled) {
		return Status{Problems: []string{ErrNotInstalled.Error()}}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return m.checkState(ctx, state)
}

func (m *Manager) checkState(ctx context.Context, state storedState) (Status, error) {
	s := Status{Installed: true}
	material, err := materialFromBundle(state.Current)
	if err != nil {
		s.Problems = append(s.Problems, err.Error())
		return s, nil
	}
	s.RootFingerprint = fingerprint(material.rootCert)
	s.LeafNotAfter = material.leafCert.NotAfter
	if err := validateMaterial(material, m.cfg); err != nil {
		s.Problems = append(s.Problems, err.Error())
	}
	s.NeedsRotation = leafNeedsRenewal(material, m.cfg.now(), m.cfg.RenewBefore) || rootNeedsRotation(material, m.cfg)
	identityCurrent, identityErr := servingIdentityMatches(m.cfg.IdentityPath, state.Current)
	if identityErr != nil {
		s.Problems = append(s.Problems, identityErr.Error())
	} else if !identityCurrent {
		s.Problems = append(s.Problems, "serving identity does not match the saved certificate")
	}
	if state.Pending != nil {
		s.Problems = append(s.Problems, "certificate rotation is pending recovery")
	}
	if len(state.StaleRoots) != 0 {
		s.Problems = append(s.Problems, "old CA trust is pending cleanup")
	}
	trusted, err := m.trust.Trusted(ctx, state.Current.RootCertPEM, state.Current.CertPEM, firstDNSName(m.cfg.Hosts))
	if err != nil {
		return s, fmt.Errorf("check native trust: %w", err)
	}
	s.Trusted = trusted
	if !trusted {
		s.Problems = append(s.Problems, "development CA is not trusted by the native store")
	}
	s.Usable = s.Trusted && !s.NeedsRotation && len(s.Problems) == 0
	return s, nil
}

func (m *Manager) Rotate(ctx context.Context) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.load(ctx)
	if errors.Is(err, ErrNotInstalled) {
		return m.installLocked(ctx)
	}
	if err != nil {
		return Result{}, err
	}
	return m.rotateFrom(ctx, state)
}

func (m *Manager) rotateFrom(ctx context.Context, state storedState) (Result, error) {
	next, err := Generate(m.cfg)
	if err != nil {
		return Result{}, err
	}
	state.Pending = &next
	if err := m.saveState(ctx, state); err != nil {
		return Result{}, fmt.Errorf("save pending certificate rotation: %w", err)
	}
	state, err = m.finishPendingRotation(ctx, state)
	if err != nil {
		return Result{}, err
	}
	cleanupErr := m.cleanupStale(ctx, &state)
	status, checkErr := m.checkState(ctx, state)
	result := Result{Status: status, Bundle: cloneBundle(state.Current), Changed: true, Rotated: true}
	if checkErr != nil {
		return result, checkErr
	}
	if cleanupErr != nil {
		return result, cleanupErr
	}
	return result, nil
}

func (m *Manager) finishPendingRotation(ctx context.Context, state storedState) (storedState, error) {
	if state.Pending == nil {
		return state, nil
	}
	if err := m.trust.Install(ctx, state.Pending.RootCertPEM); err != nil {
		return state, fmt.Errorf("install replacement native trust: %w", err)
	}
	// Publish the new leaf before retiring old trust. If committing the secret
	// then fails, both roots remain trusted and the pending journal is retryable.
	if err := writeServingIdentity(m.cfg.IdentityPath, *state.Pending); err != nil {
		return state, err
	}
	oldRoot := cloneBytes(state.Current.RootCertPEM)
	state.Current = cloneBundle(*state.Pending)
	state.Pending = nil
	if _, err := parseCertificate(oldRoot); err == nil {
		state.StaleRoots = append(state.StaleRoots, oldRoot)
	}
	if err := m.saveState(ctx, state); err != nil {
		return state, fmt.Errorf("commit replacement certificate secret: %w", err)
	}
	return state, nil
}

func (m *Manager) cleanupStale(ctx context.Context, state *storedState) error {
	for len(state.StaleRoots) != 0 {
		root := state.StaleRoots[0]
		if err := m.trust.Remove(ctx, root); err != nil {
			return fmt.Errorf("replacement installed but old CA cleanup failed: %w", err)
		}
		state.StaleRoots = state.StaleRoots[1:]
		if err := m.saveState(ctx, *state); err != nil {
			return fmt.Errorf("save old CA cleanup: %w", err)
		}
	}
	return nil
}

func (m *Manager) Remove(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.load(ctx)
	if errors.Is(err, ErrNotInstalled) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.cleanupStale(ctx, &state); err != nil {
		return err
	}
	if err := m.trust.Remove(ctx, state.Current.RootCertPEM); err != nil {
		return fmt.Errorf("remove native trust: %w", err)
	}
	if err := removeServingIdentity(m.cfg.IdentityPath); err != nil {
		return fmt.Errorf("remove serving identity: %w", err)
	}
	if err := m.cfg.Secrets.Delete(ctx, m.cfg.SecretKey); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return fmt.Errorf("delete certificate secret: %w", err)
	}
	return nil
}

func (m *Manager) load(ctx context.Context) (storedState, error) {
	data, err := m.cfg.Secrets.Get(ctx, m.cfg.SecretKey)
	if errors.Is(err, secrets.ErrNotFound) {
		return storedState{}, ErrNotInstalled
	}
	if err != nil {
		return storedState{}, fmt.Errorf("load certificate secret %q: %w", m.cfg.SecretKey, err)
	}
	var state storedState
	if err := json.Unmarshal(data, &state); err != nil {
		return storedState{}, fmt.Errorf("decode certificate secret %q: %w", m.cfg.SecretKey, err)
	}
	if state.Version != stateVersion {
		return storedState{}, fmt.Errorf("certificate secret %q has unsupported version %d", m.cfg.SecretKey, state.Version)
	}
	return state, nil
}

func (m *Manager) saveState(ctx context.Context, state storedState) error {
	state.Version = stateVersion
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode certificate secret: %w", err)
	}
	if err := m.cfg.Secrets.Set(ctx, m.cfg.SecretKey, data); err != nil {
		return fmt.Errorf("save certificate secret %q: %w", m.cfg.SecretKey, err)
	}
	return nil
}

func applyDefaultsAndValidate(cfg *Config) error {
	if cfg.Name == "" {
		cfg.Name = defaultName
	}
	if len(cfg.Hosts) == 0 {
		cfg.Hosts = []string{"localhost", "127.0.0.1", "::1"}
	}
	if cfg.CAValidity == 0 {
		cfg.CAValidity = 5 * 365 * 24 * time.Hour
	}
	if cfg.Validity == 0 {
		cfg.Validity = 90 * 24 * time.Hour
	}
	if cfg.RenewBefore == 0 {
		cfg.RenewBefore = 14 * 24 * time.Hour
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.rand == nil {
		cfg.rand = rand.Reader
	}
	if cfg.Scope > ScopeSystem {
		return fmt.Errorf("devtls: invalid Scope %d", cfg.Scope)
	}
	if cfg.Privilege > PrivilegeAlreadyElevated {
		return fmt.Errorf("devtls: invalid PrivilegeMode %d", cfg.Privilege)
	}
	if cfg.CAValidity <= 0 || cfg.Validity <= 0 || cfg.RenewBefore < 0 {
		return errors.New("devtls: certificate durations must be positive")
	}
	if cfg.RenewBefore >= cfg.Validity {
		return errors.New("devtls: RenewBefore must be shorter than Validity")
	}
	if cfg.CAValidity <= cfg.Validity+cfg.RenewBefore {
		return errors.New("devtls: CAValidity must exceed Validity plus RenewBefore")
	}
	return validateHosts(cfg.Hosts)
}

func resolvedScope(scope Scope) Scope {
	if scope != ScopeAuto {
		return scope
	}
	if runtime.GOOS == "linux" {
		return ScopeSystem
	}
	return ScopeUser
}

func validateHosts(hosts []string) error {
	seen := map[string]bool{}
	for _, host := range hosts {
		if host == "" || strings.ContainsAny(host, "/\\") {
			return fmt.Errorf("devtls: invalid host %q", host)
		}
		if seen[host] {
			return fmt.Errorf("devtls: duplicate host %q", host)
		}
		seen[host] = true
	}
	return nil
}

func firstDNSName(hosts []string) string {
	for _, host := range hosts {
		if net.ParseIP(host) == nil {
			return host
		}
	}
	return "localhost"
}

func cloneBytes(in []byte) []byte { return append([]byte(nil), in...) }
func cloneBundle(b Bundle) Bundle {
	return Bundle{RootCertPEM: cloneBytes(b.RootCertPEM), RootKeyPEM: cloneBytes(b.RootKeyPEM), CertPEM: cloneBytes(b.CertPEM), KeyPEM: cloneBytes(b.KeyPEM)}
}
