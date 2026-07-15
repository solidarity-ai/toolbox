//go:build linux

package devtls

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type linuxStore struct {
	privilege  PrivilegeMode
	runner     commandRunner
	anchor     string
	bundle     string
	update     string
	updateArgs []string
}

func newNativeStore(cfg nativeStoreConfig) (TrustStore, error) {
	if cfg.scope == ScopeUser {
		return nil, fmt.Errorf("%w: Linux has no portable per-user TLS trust store; use ScopeSystem or an application-specific store", ErrUnsupportedScope)
	}
	r := runnerOrDefault(cfg.runner)
	base := safeAnchorName(cfg.name) + ".crt"
	if command, err := r.LookPath("update-ca-certificates"); err == nil {
		return &linuxStore{
			privilege: cfg.privilege, runner: r,
			anchor: filepath.Join("/usr/local/share/ca-certificates", base),
			bundle: "/etc/ssl/certs/ca-certificates.crt",
			update: command,
		}, nil
	}
	if command, err := r.LookPath("update-ca-trust"); err == nil {
		return &linuxStore{
			privilege: cfg.privilege, runner: r,
			anchor: filepath.Join("/etc/pki/ca-trust/source/anchors", base),
			bundle: "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
			update: command, updateArgs: []string{"extract"},
		}, nil
	}
	return nil, errors.New("devtls: neither update-ca-certificates nor update-ca-trust is installed")
}

func (s *linuxStore) Trusted(_ context.Context, rootCertPEM, leafCertPEM []byte, hostname string) (bool, error) {
	equal, err := certPEMMatchesFile(rootCertPEM, s.anchor)
	if err != nil || !equal {
		return equal, err
	}
	return bundleTrusts(s.bundle, leafCertPEM, hostname)
}

func (s *linuxStore) Install(ctx context.Context, rootCertPEM []byte) error {
	rootCertPath, _, cleanup, err := writePublicCerts(rootCertPEM, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := runPrivileged(ctx, s.runner, s.privilege, "install", "-m", "0644", rootCertPath, s.anchor); err != nil {
		return err
	}
	_, err = runPrivileged(ctx, s.runner, s.privilege, s.update, s.updateArgs...)
	return err
}

func (s *linuxStore) Remove(ctx context.Context, rootCertPEM []byte) error {
	equal, err := certPEMMatchesFile(rootCertPEM, s.anchor)
	if err != nil || !equal {
		return err
	}
	if _, err := runPrivileged(ctx, s.runner, s.privilege, "rm", "-f", s.anchor); err != nil {
		return err
	}
	_, err = runPrivileged(ctx, s.runner, s.privilege, s.update, s.updateArgs...)
	return err
}

var unsafeAnchorChar = regexp.MustCompile(`[^a-z0-9._-]+`)

func safeAnchorName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = unsafeAnchorChar.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	if name == "" {
		return "toolbox-local-development-ca"
	}
	return name
}
