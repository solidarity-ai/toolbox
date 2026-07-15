package devtls

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type nativeStoreConfig struct {
	name      string
	scope     Scope
	privilege PrivilegeMode
	runner    commandRunner
}

type commandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, &CommandError{Command: append([]string{name}, args...), Output: strings.TrimSpace(string(output)), Err: err}
	}
	return output, nil
}

// CommandError reports a failed native trust command. Command never contains a
// private-key path or secret.
type CommandError struct {
	Command []string
	Output  string
	Err     error
}

func (e *CommandError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("%s: %v", strings.Join(e.Command, " "), e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", strings.Join(e.Command, " "), e.Err, e.Output)
}

func (e *CommandError) Unwrap() error { return e.Err }

func runnerOrDefault(r commandRunner) commandRunner {
	if r == nil {
		return execRunner{}
	}
	return r
}

func runPrivileged(ctx context.Context, r commandRunner, mode PrivilegeMode, name string, args ...string) ([]byte, error) {
	if mode == PrivilegeAlreadyElevated {
		return r.Run(ctx, name, args...)
	}
	sudo, err := r.LookPath("sudo")
	if err != nil {
		return nil, fmt.Errorf("system trust requires root; sudo is unavailable (rerun elevated or use PrivilegeAlreadyElevated): %w", err)
	}
	return r.Run(ctx, sudo, append([]string{name}, args...)...)
}

func writePublicCerts(rootPEM, leafPEM []byte) (rootPath, leafPath string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "toolbox-devtls-public-*")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	rootPath = filepath.Join(dir, "root-ca.pem")
	if err := os.WriteFile(rootPath, rootPEM, 0o644); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	if len(leafPEM) != 0 {
		leafPath = filepath.Join(dir, "localhost.pem")
		if err := os.WriteFile(leafPath, leafPEM, 0o644); err != nil {
			cleanup()
			return "", "", func() {}, err
		}
	}
	return rootPath, leafPath, cleanup, nil
}

func certPEMMatchesFile(certPEM []byte, path string) (bool, error) {
	storedPEM, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	aCert, err := parseCertificate(certPEM)
	if err != nil {
		return false, err
	}
	bCert, err := parseCertificate(storedPEM)
	if err != nil {
		return false, err
	}
	return bytes.Equal(aCert.Raw, bCert.Raw), nil
}

func bundleTrusts(bundlePath string, leafPEM []byte, hostname string) (bool, error) {
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	leaf, err := parseCertificate(leafPEM)
	if err != nil {
		return false, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		return false, fmt.Errorf("system CA bundle %s contains no parseable certificates", bundlePath)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: hostname, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	if err != nil {
		return false, nil
	}
	return true, nil
}
