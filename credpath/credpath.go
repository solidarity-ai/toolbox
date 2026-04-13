// Package credpath centralizes secret store key construction for credentials.
//
// The convention is:
//
//	Shared values:    {module}/{cred}/{suffix}
//	Per-account values: {module}/{cred}/accounts/{account}/{suffix}
package credpath

// Shared returns the secret store key for a shared (per-app) credential value.
// e.g., Shared("google-workspace", "gws", "client_id") → "google-workspace/gws/client_id"
func Shared(module, cred, suffix string) string {
	return module + "/" + cred + "/" + suffix
}

// Account returns the secret store key for a per-account credential value.
// e.g., Account("google-workspace", "gws", "admin", "refresh_token") → "google-workspace/gws/accounts/admin/refresh_token"
func Account(module, cred, account, suffix string) string {
	return module + "/" + cred + "/accounts/" + account + "/" + suffix
}

// AccountPrefix returns the prefix for all secrets under an account.
// e.g., AccountPrefix("google-workspace", "gws", "admin") → "google-workspace/gws/accounts/admin/"
func AccountPrefix(module, cred, account string) string {
	return module + "/" + cred + "/accounts/" + account + "/"
}

// SharedPrefix returns the prefix for shared secrets.
// e.g., SharedPrefix("google-workspace", "gws") → "google-workspace/gws/"
func SharedPrefix(module, cred string) string {
	return module + "/" + cred + "/"
}

// AccountsPrefix returns the prefix for listing all accounts under a credential.
// e.g., AccountsPrefix("google-workspace", "gws") → "google-workspace/gws/accounts/"
func AccountsPrefix(module, cred string) string {
	return module + "/" + cred + "/accounts/"
}

// --- OAuth2 type-aware helpers ---

// OAuth2ClientID returns the key for an OAuth2 client ID (shared, per-app).
func OAuth2ClientID(module, cred string) string {
	return Shared(module, cred, "client_id")
}

// OAuth2ClientSecret returns the key for an OAuth2 client secret (shared, per-app).
func OAuth2ClientSecret(module, cred string) string {
	return Shared(module, cred, "client_secret")
}

// OAuth2RefreshToken returns the key for an OAuth2 refresh token (per-account).
func OAuth2RefreshToken(module, cred, account string) string {
	return Account(module, cred, account, "refresh_token")
}

// --- API key type-aware helper ---

// APIKey returns the key for an API key (per-account).
func APIKey(module, cred, account string) string {
	return Account(module, cred, account, "api_key")
}

// --- Bearer type-aware helpers ---

// BearerToken returns the key for a bearer token (per-account).
func BearerToken(module, cred, account string) string {
	return Account(module, cred, account, "token")
}

// BearerUsername returns the key for a basic-auth username (per-account).
func BearerUsername(module, cred, account string) string {
	return Account(module, cred, account, "username")
}

// BearerPassword returns the key for a basic-auth password (per-account).
func BearerPassword(module, cred, account string) string {
	return Account(module, cred, account, "password")
}
