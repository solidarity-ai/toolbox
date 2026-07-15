package devtls

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadTLSCertificate loads a daemon serving identity without accessing the CA
// secret store. On Windows the file is transparently unprotected with DPAPI.
func LoadTLSCertificate(path string) (tls.Certificate, error) {
	data, err := readIdentityFile(path)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load development TLS identity %q: %w", path, err)
	}
	plain, err := unprotectIdentity(data)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("unprotect development TLS identity %q: %w", path, err)
	}
	certificate, err := tls.X509KeyPair(plain, plain)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse development TLS identity %q: %w", path, err)
	}
	return certificate, nil
}

// LoadTLSConfig returns a server configuration backed only by the restricted
// serving identity on disk. It can be called while SecretStore is locked.
func LoadTLSConfig(path string) (*tls.Config, error) {
	certificate, err := LoadTLSCertificate(path)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func writeServingIdentity(path string, bundle Bundle) error {
	if _, err := bundle.TLSCertificate(); err != nil {
		return fmt.Errorf("validate serving identity: %w", err)
	}
	plain := make([]byte, 0, len(bundle.CertPEM)+len(bundle.KeyPEM)+1)
	plain = append(plain, bundle.CertPEM...)
	if len(plain) != 0 && plain[len(plain)-1] != '\n' {
		plain = append(plain, '\n')
	}
	plain = append(plain, bundle.KeyPEM...)
	data, err := protectIdentity(plain)
	if err != nil {
		return fmt.Errorf("protect serving identity: %w", err)
	}
	dir := filepath.Dir(path)
	if err := secureIdentityDirectory(dir); err != nil {
		return fmt.Errorf("secure serving identity directory %q: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".devtls-identity-*")
	if err != nil {
		return fmt.Errorf("create temporary serving identity: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary serving identity: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary serving identity: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary serving identity: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary serving identity: %w", err)
	}
	if err := replaceIdentityFile(tempPath, path); err != nil {
		return fmt.Errorf("publish serving identity: %w", err)
	}
	committed = true
	return nil
}

func servingIdentityMatches(path string, bundle Bundle) (bool, error) {
	certificate, err := LoadTLSCertificate(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want, err := bundle.TLSCertificate()
	if err != nil {
		return false, err
	}
	if len(certificate.Certificate) == 0 || len(want.Certificate) == 0 {
		return false, nil
	}
	return bytes.Equal(certificate.Certificate[0], want.Certificate[0]), nil
}

func removeServingIdentity(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
