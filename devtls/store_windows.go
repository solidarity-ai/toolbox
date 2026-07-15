//go:build windows

package devtls

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

type windowsStore struct {
	scope  Scope
	runner commandRunner
}

func newNativeStore(cfg nativeStoreConfig) (TrustStore, error) {
	r := runnerOrDefault(cfg.runner)
	if _, err := r.LookPath("certutil.exe"); err != nil {
		return nil, fmt.Errorf("find Windows certutil.exe: %w", err)
	}
	return &windowsStore{scope: cfg.scope, runner: r}, nil
}

func (s *windowsStore) args(args ...string) []string {
	if s.scope == ScopeUser {
		return append([]string{"-user"}, args...)
	}
	return args
}

func (s *windowsStore) Trusted(ctx context.Context, rootCertPEM, _ []byte, _ string) (bool, error) {
	id, err := windowsCertID(rootCertPEM)
	if err != nil {
		return false, err
	}
	_, err = s.runner.Run(ctx, "certutil.exe", s.args("-silent", "-verifystore", "Root", id)...)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, nil
}

func (s *windowsStore) Install(ctx context.Context, rootCertPEM []byte) error {
	rootCertPath, _, cleanup, err := writePublicCerts(rootCertPEM, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = s.runner.Run(ctx, "certutil.exe", s.args("-f", "-addstore", "Root", rootCertPath)...)
	if err != nil && s.scope == ScopeSystem {
		return fmt.Errorf("machine Root store requires an administrator process (Windows UAC is not launched automatically): %w", err)
	}
	return err
}

func (s *windowsStore) Remove(ctx context.Context, rootCertPEM []byte) error {
	id, err := windowsCertID(rootCertPEM)
	if err != nil {
		return err
	}
	_, err = s.runner.Run(ctx, "certutil.exe", s.args("-f", "-delstore", "Root", id)...)
	if err != nil && s.scope == ScopeSystem {
		return fmt.Errorf("machine Root store requires an administrator process (Windows UAC is not launched automatically): %w", err)
	}
	return err
}

func windowsCertID(certPEM []byte) (string, error) {
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(cert.Raw) // Windows certificate store's native CertId format.
	return hex.EncodeToString(sum[:]), nil
}
