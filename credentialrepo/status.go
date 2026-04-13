package credentialrepo

import (
	"context"
	"fmt"
	"strings"

	"github.com/solidarity-ai/toolbox/credpath"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

// SecretCheck describes whether a specific secret ref is present.
type SecretCheck struct {
	Label    string
	Ref      Ref
	Present  bool
	Required bool
}

// AccountCheck describes the account-scoped secret checks for one account.
type AccountCheck struct {
	Account        string
	MissingAccount bool
	Secrets        []SecretCheck
}

// CredentialCheck describes configured secret state for one package credential.
type CredentialCheck struct {
	Credential tooldef.PackageCredential
	Shared     []SecretCheck
	Accounts   []AccountCheck
}

// Configured reports whether all required shared and account secrets are present.
func (c CredentialCheck) Configured() bool {
	for _, check := range c.Shared {
		if check.Required && !check.Present {
			return false
		}
	}
	if len(c.Accounts) == 0 {
		return false
	}
	for _, acct := range c.Accounts {
		if acct.MissingAccount {
			return false
		}
		for _, check := range acct.Secrets {
			if !check.Present {
				return false
			}
		}
	}
	return true
}

// PackageCheck groups per-credential secret status for a package.
type PackageCheck struct {
	Credentials []CredentialCheck
}

// RenameResult reports how many secrets moved for one credential during an account rename.
type RenameResult struct {
	CredentialName string
	Moved          int
}

// DeleteResult reports how many secrets were removed for one credential.
type DeleteResult struct {
	CredentialName string
	Deleted        int
}

// DiscoverCredentialAccounts inspects the repository store to determine which
// accounts have been authorized for each credential in the package.
func (r *Repository) DiscoverCredentialAccounts(ctx context.Context, pkg tooldef.Package) (map[string][]string, error) {
	return DiscoverCredentialAccounts(ctx, pkg, r.store)
}

// CheckPackage builds a structured view of the package's configured secret state.
func (r *Repository) CheckPackage(ctx context.Context, pkg tooldef.Package) (PackageCheck, error) {
	credAccounts, err := r.DiscoverCredentialAccounts(ctx, pkg)
	if err != nil {
		return PackageCheck{}, fmt.Errorf("discovering accounts: %w", err)
	}

	result := PackageCheck{Credentials: make([]CredentialCheck, 0, len(pkg.Credentials))}
	for _, cred := range pkg.Credentials {
		check := CredentialCheck{Credential: cred}

		switch cred.Type {
		case "oauth2":
			check.Shared = r.checkRefs(ctx, []labeledRef{
				{label: "client_id", ref: OAuth2ClientIDRef(pkg, cred.Name)},
				{label: "client_secret", ref: OAuth2ClientSecretRef(pkg, cred.Name), optional: cred.Provider.PKCEEnabled()},
			})
			check.Accounts = r.accountChecks(ctx, pkg, cred.Name, credAccounts[cred.Name], func(acct string) []labeledRef {
				return []labeledRef{
					{label: "refresh_token", ref: OAuth2RefreshTokenRef(pkg, cred.Name, acct)},
				}
			})
		case "api_key":
			check.Accounts = r.accountChecks(ctx, pkg, cred.Name, credAccounts[cred.Name], func(acct string) []labeledRef {
				return []labeledRef{
					{label: "api_key", ref: APIKeyRef(pkg, cred.Name, acct)},
				}
			})
		case "bearer":
			if cred.Inject.Method == "basic_auth" {
				check.Accounts = r.accountChecks(ctx, pkg, cred.Name, credAccounts[cred.Name], func(acct string) []labeledRef {
					return []labeledRef{
						{label: "username", ref: BearerUsernameRef(pkg, cred.Name, acct)},
						{label: "password", ref: BearerPasswordRef(pkg, cred.Name, acct)},
					}
				})
			} else {
				check.Accounts = r.accountChecks(ctx, pkg, cred.Name, credAccounts[cred.Name], func(acct string) []labeledRef {
					return []labeledRef{
						{label: "token", ref: BearerTokenRef(pkg, cred.Name, acct)},
					}
				})
			}
		}

		result.Credentials = append(result.Credentials, check)
	}

	return result, nil
}

// RenameAccount renames account-scoped secrets for one or all credentials in a package.
func (r *Repository) RenameAccount(ctx context.Context, pkg tooldef.Package, credentialName, oldName, newName string) ([]RenameResult, error) {
	if err := transport.ValidateAccountString(oldName); err != nil {
		return nil, fmt.Errorf("invalid old account name: %w", err)
	}
	if err := transport.ValidateAccountString(newName); err != nil {
		return nil, fmt.Errorf("invalid new account name: %w", err)
	}
	if oldName == newName {
		return nil, fmt.Errorf("old and new account names must differ")
	}

	creds, err := selectCredentials(pkg, credentialName)
	if err != nil {
		return nil, err
	}

	type renamePlan struct {
		credentialName string
		oldPrefix      string
		newPrefix      string
		keys           []string
	}

	plans := make([]renamePlan, 0, len(creds))
	for _, cred := range creds {
		oldPrefix := AccountsPrefix(pkg, cred.Name) + oldName + "/"
		keys, err := r.List(ctx, oldPrefix)
		if err != nil {
			return nil, fmt.Errorf("listing secrets for %s/%s: %w", cred.Name, oldName, err)
		}
		if len(keys) == 0 {
			continue
		}

		newPrefix := AccountsPrefix(pkg, cred.Name) + newName + "/"
		destKeys, err := r.List(ctx, newPrefix)
		if err != nil {
			return nil, fmt.Errorf("listing secrets for %s/%s: %w", cred.Name, newName, err)
		}
		if len(destKeys) > 0 {
			return nil, fmt.Errorf("account %q already exists for credential %s", newName, cred.Name)
		}

		plans = append(plans, renamePlan{
			credentialName: cred.Name,
			oldPrefix:      oldPrefix,
			newPrefix:      newPrefix,
			keys:           keys,
		})
	}

	var results []RenameResult
	for _, plan := range plans {
		for _, key := range plan.keys {
			suffix := strings.TrimPrefix(key, plan.oldPrefix)
			value, err := r.Get(ctx, Ref(key))
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", key, err)
			}
			newKey := Ref(plan.newPrefix + suffix)
			if err := r.Set(ctx, newKey, value); err != nil {
				return nil, fmt.Errorf("writing %s: %w", newKey, err)
			}
			if err := r.Delete(ctx, Ref(key)); err != nil {
				return nil, fmt.Errorf("deleting %s: %w", key, err)
			}
		}

		results = append(results, RenameResult{
			CredentialName: plan.credentialName,
			Moved:          len(plan.keys),
		})
	}

	return results, nil
}

// DeleteAccount removes all account-scoped secrets for one account across one
// credential or all credentials in a package.
func (r *Repository) DeleteAccount(ctx context.Context, pkg tooldef.Package, credentialName, account string) ([]DeleteResult, error) {
	if err := transport.ValidateAccountString(account); err != nil {
		return nil, fmt.Errorf("invalid account name: %w", err)
	}

	creds, err := selectCredentials(pkg, credentialName)
	if err != nil {
		return nil, err
	}

	var results []DeleteResult
	for _, cred := range creds {
		deleted, err := r.deleteByPrefix(ctx, AccountsPrefix(pkg, cred.Name)+account+"/")
		if err != nil {
			return nil, fmt.Errorf("deleting secrets for %s/%s: %w", cred.Name, account, err)
		}
		if deleted == 0 {
			continue
		}
		results = append(results, DeleteResult{
			CredentialName: cred.Name,
			Deleted:        deleted,
		})
	}

	return results, nil
}

// DeleteCredential removes all shared and account-scoped secrets for one credential.
func (r *Repository) DeleteCredential(ctx context.Context, pkg tooldef.Package, credentialName string) (DeleteResult, error) {
	if credentialName == "" {
		return DeleteResult{}, fmt.Errorf("credential name cannot be empty")
	}

	creds, err := selectCredentials(pkg, credentialName)
	if err != nil {
		return DeleteResult{}, err
	}

	cred := creds[0]
	deleted, err := r.deleteByPrefix(ctx, credpath.SharedPrefix(pkg.Module.String(), cred.Name))
	if err != nil {
		return DeleteResult{}, fmt.Errorf("deleting secrets for %s: %w", cred.Name, err)
	}

	return DeleteResult{
		CredentialName: cred.Name,
		Deleted:        deleted,
	}, nil
}

type labeledRef struct {
	label    string
	ref      Ref
	optional bool
}

func (r *Repository) checkRefs(ctx context.Context, refs []labeledRef) []SecretCheck {
	checks := make([]SecretCheck, 0, len(refs))
	for _, ref := range refs {
		value, err := r.Get(ctx, ref.ref)
		checks = append(checks, SecretCheck{
			Label:    ref.label,
			Ref:      ref.ref,
			Present:  err == nil && len(value) > 0,
			Required: !ref.optional,
		})
	}
	return checks
}

func (r *Repository) accountChecks(ctx context.Context, pkg tooldef.Package, credName string, accounts []string, refs func(string) []labeledRef) []AccountCheck {
	if len(accounts) == 0 {
		return []AccountCheck{{
			Account:        "default",
			MissingAccount: true,
			Secrets:        r.checkRefs(ctx, refs("default")),
		}}
	}

	checks := make([]AccountCheck, 0, len(accounts))
	for _, account := range accounts {
		checks = append(checks, AccountCheck{
			Account: account,
			Secrets: r.checkRefs(ctx, refs(account)),
		})
	}
	return checks
}

func (r *Repository) deleteByPrefix(ctx context.Context, prefix string) (int, error) {
	keys, err := r.List(ctx, prefix)
	if err != nil {
		return 0, err
	}

	for _, key := range keys {
		if err := r.Delete(ctx, Ref(key)); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

func selectCredentials(pkg tooldef.Package, credentialName string) ([]tooldef.PackageCredential, error) {
	if credentialName == "" {
		return pkg.Credentials, nil
	}

	for _, cred := range pkg.Credentials {
		if cred.Name == credentialName {
			return []tooldef.PackageCredential{cred}, nil
		}
	}

	return nil, fmt.Errorf("credential %q not found in package", credentialName)
}
