package credentialrepo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

// Repository loads and stores package credential state on top of a SecretStore.
type Repository struct {
	store secrets.SecretStore
}

type Ref string

func New(store secrets.SecretStore) *Repository {
	return &Repository{store: store}
}

func (r Ref) String() string { return string(r) }

func (r *Repository) Get(ctx context.Context, ref Ref) ([]byte, error) {
	return r.store.Get(ctx, ref.String())
}

func (r *Repository) Set(ctx context.Context, ref Ref, value []byte) error {
	return r.store.Set(ctx, ref.String(), value)
}

func (r *Repository) Delete(ctx context.Context, ref Ref) error {
	return r.store.Delete(ctx, ref.String())
}

func (r *Repository) List(ctx context.Context, prefix string) ([]string, error) {
	return r.store.List(ctx, prefix)
}

// PackageCredentialPolicy implements toolset.PackageCredentialPolicySource.
func (r *Repository) PackageCredentialPolicy(ctx context.Context, pkg tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	accounts, err := DiscoverCredentialAccounts(ctx, pkg, r.store)
	if err != nil {
		return toolset.PackageCredentialPolicy{}, err
	}

	var injector *transport.CredentialInjector
	rules := BuildInjectionRules(pkg)
	if len(rules) > 0 {
		injector = transport.NewCredentialInjector(rules, r.store)
	}

	var allowlist *transport.HostAllowlist
	if len(pkg.AllowedHosts) > 0 {
		allowlist = transport.NewHostAllowlist(pkg.AllowedHosts)
	}

	return toolset.PackageCredentialPolicy{
		CredentialAccounts: accounts,
		Injector:           injector,
		Allowlist:          allowlist,
	}, nil
}

// StaticPolicySource is a fixed map-backed PackageCredentialPolicySource useful in tests.
type StaticPolicySource map[tooldef.ModulePath]toolset.PackageCredentialPolicy

func (s StaticPolicySource) PackageCredentialPolicy(_ context.Context, pkg tooldef.Package) (toolset.PackageCredentialPolicy, error) {
	if policy, ok := s[pkg.Module]; ok {
		return policy, nil
	}
	return toolset.PackageCredentialPolicy{}, nil
}

// BuildInjectionRules converts package credential declarations into transport
// injection rules using the package's canonical module path as the secret prefix.
func BuildInjectionRules(pkg tooldef.Package) []transport.InjectionRule {
	moduleName := pkg.Module.String()
	if strings.Contains(moduleName, "..") || strings.HasPrefix(moduleName, "/") {
		return nil
	}
	rules := make([]transport.InjectionRule, 0, len(pkg.Credentials))
	for _, cred := range pkg.Credentials {
		if strings.Contains(cred.Name, "..") || strings.Contains(cred.Name, "/") {
			continue
		}
		rule := transport.InjectionRule{
			Hosts:                    cred.Inject.Hosts,
			PathPrefix:               cred.Inject.PathPrefix,
			ModuleName:               moduleName,
			CredentialName:           cred.Name,
			SecretPrefix:             credpath.SharedPrefix(moduleName, cred.Name),
			Type:                     transport.CredentialType(cred.Type),
			Method:                   transport.InjectionMethod(cred.Inject.Method),
			HeaderName:               cred.Inject.HeaderName,
			AllowUnsafeHTTPInjection: cred.Inject.AllowUnsafeHTTPInjection,
		}
		if cred.Provider != nil {
			rule.Provider = resolveProvider(cred.Provider)
		}
		rules = append(rules, rule)
	}
	return rules
}

// DiscoverCredentialAccounts inspects the secret store to determine which
// accounts have been authorized for each credential in the package.
func DiscoverCredentialAccounts(ctx context.Context, pkg tooldef.Package, store secrets.SecretStore) (map[string][]string, error) {
	result := make(map[string][]string, len(pkg.Credentials))
	moduleName := pkg.Module.String()
	for _, cred := range pkg.Credentials {
		prefix := credpath.AccountsPrefix(moduleName, cred.Name)
		keys, err := store.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("listing accounts for credential %s: %w", cred.Name, err)
		}

		seen := make(map[string]bool)
		for _, key := range keys {
			rest := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(rest, "/"); idx > 0 {
				seen[rest[:idx]] = true
			}
		}

		accounts := make([]string, 0, len(seen))
		for acct := range seen {
			accounts = append(accounts, acct)
		}
		sort.Strings(accounts)
		result[cred.Name] = accounts
	}
	return result, nil
}

func OAuth2ClientIDRef(pkg tooldef.Package, credName string) Ref {
	return Ref(credpath.OAuth2ClientID(pkg.Module.String(), credName))
}

func OAuth2ClientSecretRef(pkg tooldef.Package, credName string) Ref {
	return Ref(credpath.OAuth2ClientSecret(pkg.Module.String(), credName))
}

func OAuth2RefreshTokenRef(pkg tooldef.Package, credName, account string) Ref {
	return Ref(credpath.OAuth2RefreshToken(pkg.Module.String(), credName, account))
}

func APIKeyRef(pkg tooldef.Package, credName, account string) Ref {
	return Ref(credpath.APIKey(pkg.Module.String(), credName, account))
}

func BearerTokenRef(pkg tooldef.Package, credName, account string) Ref {
	return Ref(credpath.BearerToken(pkg.Module.String(), credName, account))
}

func BearerUsernameRef(pkg tooldef.Package, credName, account string) Ref {
	return Ref(credpath.BearerUsername(pkg.Module.String(), credName, account))
}

func BearerPasswordRef(pkg tooldef.Package, credName, account string) Ref {
	return Ref(credpath.BearerPassword(pkg.Module.String(), credName, account))
}

func AccountsPrefix(pkg tooldef.Package, credName string) string {
	return credpath.AccountsPrefix(pkg.Module.String(), credName)
}

func resolveProvider(cfg *tooldef.OAuth2ProviderConfig) *transport.OAuth2Provider {
	if cfg.Name != "" {
		if known, ok := transport.KnownProviders[cfg.Name]; ok {
			return &known
		}
	}
	if cfg.AuthURL != "" || cfg.TokenURL != "" {
		return &transport.OAuth2Provider{
			AuthURL:  cfg.AuthURL,
			TokenURL: cfg.TokenURL,
		}
	}
	return nil
}
