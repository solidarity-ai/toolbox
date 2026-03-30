package tool

import (
	"fmt"
	"strings"
)

const tenantSecretNamespaceSegment = "tenant"

// PackageSecretNamespace returns the package-scoped secret namespace root for a
// validated module path.
func PackageSecretNamespace(module ModulePath) (string, error) {
	parsed, err := ParseModulePath(module.String())
	if err != nil {
		return "", fmt.Errorf("package secret namespace: %w", err)
	}
	return parsed.String(), nil
}

// TenantSecretNamespace returns a tenant-scoped namespace nested beneath the
// package root.
func TenantSecretNamespace(module ModulePath, tenant string) (string, error) {
	base, err := PackageSecretNamespace(module)
	if err != nil {
		return "", err
	}
	tenantPart, err := normalizeSecretNamespacePart("tenant", tenant)
	if err != nil {
		return "", err
	}
	return base + "/" + tenantSecretNamespaceSegment + "/" + tenantPart, nil
}

// CredentialSecretKey returns the secret key for single-value credentials such
// as bearer tokens or API keys.
func CredentialSecretKey(module ModulePath, credentialName string) (string, error) {
	base, err := PackageSecretNamespace(module)
	if err != nil {
		return "", err
	}
	credentialPart, err := normalizeSecretNamespacePart("credential name", credentialName)
	if err != nil {
		return "", err
	}
	return base + "/" + credentialPart, nil
}

// CredentialFamilySecretKey returns a family-scoped secret key nested beneath a
// package or tenant namespace and reserved under one credential name.
func CredentialFamilySecretKey(module ModulePath, tenant string, credentialName string, family string) (string, error) {
	base, err := PackageSecretNamespace(module)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(tenant) != "" {
		base, err = TenantSecretNamespace(module, tenant)
		if err != nil {
			return "", err
		}
	}
	credentialPart, err := normalizeSecretNamespacePart("credential name", credentialName)
	if err != nil {
		return "", err
	}
	familyPart, err := normalizeSecretNamespacePart("credential family", family)
	if err != nil {
		return "", err
	}
	return base + "/" + credentialPart + "/" + familyPart, nil
}

func normalizeSecretNamespacePart(label string, raw string) (string, error) {
	part := strings.TrimSpace(raw)
	if part == "" {
		return "", fmt.Errorf("%s must not be empty", label)
	}
	if part == "." || part == ".." {
		return "", fmt.Errorf("%s %q is not allowed in secret namespaces", label, raw)
	}
	if strings.Contains(part, "/") {
		return "", fmt.Errorf("%s %q must not contain '/'", label, raw)
	}
	return part, nil
}
