//go:build darwin

package devtls

import (
	"context"
	"fmt"
)

type darwinStore struct {
	scope     Scope
	privilege PrivilegeMode
	runner    commandRunner
}

func newNativeStore(cfg nativeStoreConfig) (TrustStore, error) {
	r := runnerOrDefault(cfg.runner)
	security, err := r.LookPath("security")
	if err != nil {
		return nil, fmt.Errorf("find macOS security tool: %w", err)
	}
	_ = security
	return &darwinStore{scope: cfg.scope, privilege: cfg.privilege, runner: r}, nil
}

func (s *darwinStore) Trusted(ctx context.Context, rootCertPEM, leafCertPEM []byte, hostname string) (bool, error) {
	_, leafCertPath, cleanup, err := writePublicCerts(rootCertPEM, leafCertPEM)
	if err != nil {
		return false, err
	}
	defer cleanup()
	_, err = s.runner.Run(ctx, "/usr/bin/security", "verify-cert", "-c", leafCertPath, "-p", "ssl", "-n", hostname, "-L", "-q")
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, nil
}

func (s *darwinStore) Install(ctx context.Context, rootCertPEM []byte) error {
	rootCertPath, _, cleanup, err := writePublicCerts(rootCertPEM, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"add-trusted-cert", "-r", "trustRoot", "-p", "ssl"}
	if s.scope == ScopeSystem {
		args = append(args, "-d", "-k", "/Library/Keychains/System.keychain")
		args = append(args, rootCertPath)
		_, err := runPrivileged(ctx, s.runner, s.privilege, "/usr/bin/security", args...)
		return err
	}
	args = append(args, rootCertPath)
	_, err = s.runner.Run(ctx, "/usr/bin/security", args...)
	return err
}

func (s *darwinStore) Remove(ctx context.Context, rootCertPEM []byte) error {
	rootCertPath, _, cleanup, err := writePublicCerts(rootCertPEM, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	id, err := darwinCertID(rootCertPEM)
	if err != nil {
		return err
	}
	if s.scope == ScopeSystem {
		if _, err := runPrivileged(ctx, s.runner, s.privilege, "/usr/bin/security", "remove-trusted-cert", "-d", rootCertPath); err != nil {
			return err
		}
		_, err = runPrivileged(ctx, s.runner, s.privilege, "/usr/bin/security", "delete-certificate", "-Z", id, "/Library/Keychains/System.keychain")
		return err
	}
	// -t deletes both the certificate and its user trust settings.
	_, err = s.runner.Run(ctx, "/usr/bin/security", "delete-certificate", "-t", "-Z", id)
	return err
}

func darwinCertID(certPEM []byte) (string, error) {
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return "", err
	}
	return fingerprint(cert), nil
}
