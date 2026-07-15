package devtls

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"
)

type material struct {
	rootCert   *x509.Certificate
	rootKey    *ecdsa.PrivateKey
	leafCert   *x509.Certificate
	leafKey    *ecdsa.PrivateKey
	rootPEM    []byte
	rootKeyPEM []byte
	leafPEM    []byte
	leafKeyPEM []byte
}

func generateMaterial(cfg Config) (*material, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), cfg.rand)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	now := cfg.now().UTC()
	rootSerial, err := randomSerial(cfg.rand)
	if err != nil {
		return nil, fmt.Errorf("generate CA serial: %w", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          rootSerial,
		Subject:               pkix.Name{CommonName: cfg.Name, Organization: []string{"Toolbox local development"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(cfg.CAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          subjectKeyID(&rootKey.PublicKey),
	}
	rootDER, err := x509.CreateCertificate(cfg.rand, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, fmt.Errorf("parse generated CA certificate: %w", err)
	}
	m := &material{rootCert: rootCert, rootKey: rootKey}
	if err := encodeRoot(m); err != nil {
		return nil, err
	}
	return renewLeaf(m, cfg)
}

func renewLeaf(m *material, cfg Config) (*material, error) {
	if m == nil || m.rootCert == nil || m.rootKey == nil {
		return nil, errors.New("cannot renew leaf without CA certificate and key")
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), cfg.rand)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}
	now := cfg.now().UTC()
	notAfter := now.Add(cfg.Validity)
	if limit := m.rootCert.NotAfter.Add(-time.Minute); notAfter.After(limit) {
		notAfter = limit
	}
	leafSerial, err := randomSerial(cfg.rand)
	if err != nil {
		return nil, fmt.Errorf("generate leaf serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          leafSerial,
		Subject:               pkix.Name{CommonName: firstDNSName(cfg.Hosts), Organization: []string{"Toolbox local development"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range cfg.Hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	leafDER, err := x509.CreateCertificate(cfg.rand, template, m.rootCert, &leafKey.PublicKey, m.rootKey)
	if err != nil {
		return nil, fmt.Errorf("create leaf certificate: %w", err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("parse generated leaf certificate: %w", err)
	}
	copy := *m
	copy.leafCert = leafCert
	copy.leafKey = leafKey
	copy.leafPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, fmt.Errorf("encode leaf key: %w", err)
	}
	copy.leafKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})
	return &copy, nil
}

func encodeRoot(m *material) error {
	m.rootPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.rootCert.Raw})
	der, err := x509.MarshalPKCS8PrivateKey(m.rootKey)
	if err != nil {
		return fmt.Errorf("encode CA key: %w", err)
	}
	m.rootKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return nil
}

func materialFromBundle(bundle Bundle) (*material, error) {
	rootCert, err := parseCertificate(bundle.RootCertPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	rootKey, err := parseECDSAKey(bundle.RootKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	leafCert, err := parseCertificate(bundle.CertPEM)
	if err != nil {
		return nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	leafKey, err := parseECDSAKey(bundle.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse leaf key: %w", err)
	}
	return &material{
		rootCert: rootCert, rootKey: rootKey, leafCert: leafCert, leafKey: leafKey,
		rootPEM: bundle.RootCertPEM, rootKeyPEM: bundle.RootKeyPEM,
		leafPEM: bundle.CertPEM, leafKeyPEM: bundle.KeyPEM,
	}, nil
}

func (m *material) bundle() Bundle {
	return Bundle{RootCertPEM: cloneBytes(m.rootPEM), RootKeyPEM: cloneBytes(m.rootKeyPEM), CertPEM: cloneBytes(m.leafPEM), KeyPEM: cloneBytes(m.leafKeyPEM)}
}

func parseCertificate(p []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(p)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected exactly one PEM CERTIFICATE block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseECDSAKey(p []byte) (*ecdsa.PrivateKey, error) {
	block, rest := pem.Decode(p)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected exactly one PEM PRIVATE KEY block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ecdsaKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not ECDSA")
	}
	return ecdsaKey, nil
}

func validateMaterial(m *material, cfg Config) error {
	now := cfg.now()
	if !rootCanSign(m, now) {
		return errors.New("CA certificate is not a currently valid signing CA")
	}
	if err := validateBundleStructure(m); err != nil {
		return err
	}
	if now.Before(m.leafCert.NotBefore) || !now.Before(m.leafCert.NotAfter) {
		return errors.New("leaf certificate is outside its validity period")
	}
	for _, host := range cfg.Hosts {
		if err := m.leafCert.VerifyHostname(host); err != nil {
			return fmt.Errorf("leaf certificate SAN does not cover %s: %w", host, err)
		}
	}
	return nil
}

func validateBundleStructure(m *material) error {
	if m == nil || m.rootCert == nil || m.rootKey == nil || !m.rootCert.IsCA || m.rootCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("CA certificate is not a signing CA")
	}
	if !publicKeysEqual(&m.rootKey.PublicKey, m.rootCert.PublicKey) {
		return errors.New("CA certificate and private key do not match")
	}
	if !publicKeysEqual(&m.leafKey.PublicKey, m.leafCert.PublicKey) {
		return errors.New("leaf certificate and private key do not match")
	}
	if err := m.leafCert.CheckSignatureFrom(m.rootCert); err != nil {
		return fmt.Errorf("leaf is not signed by CA: %w", err)
	}
	return nil
}

func rootCanSign(m *material, now time.Time) bool {
	return m != nil && m.rootCert != nil && m.rootKey != nil &&
		m.rootCert.IsCA && m.rootCert.KeyUsage&x509.KeyUsageCertSign != 0 &&
		!now.Before(m.rootCert.NotBefore) && now.Before(m.rootCert.NotAfter) &&
		publicKeysEqual(&m.rootKey.PublicKey, m.rootCert.PublicKey)
}

func leafNeedsRenewal(m *material, now time.Time, renewBefore time.Duration) bool {
	if m == nil || m.leafCert == nil || m.rootCert == nil {
		return true
	}
	return !now.Before(m.leafCert.NotAfter.Add(-renewBefore)) ||
		!now.Before(m.rootCert.NotAfter.Add(-renewBefore))
}

func rootNeedsRotation(m *material, cfg Config) bool {
	if m == nil || m.rootCert == nil {
		return true
	}
	return !cfg.now().Add(cfg.Validity + cfg.RenewBefore).Before(m.rootCert.NotAfter)
}

func publicKeysEqual(a *ecdsa.PublicKey, b any) bool {
	other, ok := b.(*ecdsa.PublicKey)
	return ok && a.Equal(other)
}

func randomSerial(r io.Reader) (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	b[0] &= 0x7f
	b[0] |= 0x01
	return new(big.Int).SetBytes(b), nil
}

func subjectKeyID(publicKey any) []byte {
	der, _ := x509.MarshalPKIXPublicKey(publicKey)
	sum := sha256.Sum256(der)
	return sum[:20]
}

func fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
