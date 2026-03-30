package tooltest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/registry/testutil/emulatetest"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	googleWorkspaceModulePath      = tooldef.ModulePath("github.com/example/google-workspace")
	googleWorkspaceCredentialName  = "workspace"
	googleWorkspaceDefaultEndpoint = "https://admin.googleapis.com/admin/directory/v1/users?customer=my_customer&maxResults=1&orderBy=email"
	googleWorkspaceEndpointPath    = "/admin/directory/v1/users?customer=my_customer&maxResults=1&orderBy=email"
)

var googleWorkspaceEndpointRe = regexp.MustCompile(`const USERS_ENDPOINT = ".*";`)

type GoogleAuthHarness struct {
	Server        *emulatetest.Server
	Provider      tooldef.OAuth2ProviderRef
	FixtureDir    string
	StorePath     string
	IdentityPath  string
	RuntimeCAPath string
	Store         *secrets.LocalSecretStore
}

type GoogleDurableOAuthState struct {
	Namespace     string
	PersistedKeys []string
	RefreshToken  string
}

func googleWorkspaceFixtureDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "google-workspace")
}

// GoogleWorkspaceFixtureDir returns the source fixture directory for google-workspace.
func GoogleWorkspaceFixtureDir(t testing.TB) string {
	t.Helper()
	return googleWorkspaceFixtureDir()
}

// CopyGoogleWorkspaceFixture copies the google-workspace fixture into a temp dir.
func CopyGoogleWorkspaceFixture(t testing.TB) string {
	t.Helper()

	src := googleWorkspaceFixtureDir()
	dst := t.TempDir()
	if err := copyGoogleWorkspaceFixtureDir(src, dst); err != nil {
		t.Fatalf("copy google-workspace fixture: %v", err)
	}
	return dst
}

func copyGoogleWorkspaceFixtureDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// RewriteGoogleWorkspaceFixtureBaseURL rewrites the fixture's fetch target and
// auth-related manifest metadata for local emulate/TLS runs.
func RewriteGoogleWorkspaceFixtureBaseURL(t testing.TB, dir, baseURL string, provider tooldef.OAuth2ProviderRef) {
	t.Helper()
	if err := rewriteGoogleWorkspaceFixtureBaseURL(dir, baseURL, provider); err != nil {
		t.Fatalf("rewrite google-workspace fixture base URL: %v", err)
	}
}

func rewriteGoogleWorkspaceFixtureBaseURL(dir, baseURL string, provider tooldef.OAuth2ProviderRef) error {
	parsedBaseURL, err := parseGoogleWorkspaceBaseURL(baseURL)
	if err != nil {
		return err
	}
	toolPath := filepath.Join(dir, "tools", "users.list.ts")
	if err := rewriteGoogleWorkspaceTool(toolPath, parsedBaseURL); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "toolbox.devpkg.json")
	if err := rewriteGoogleWorkspaceManifest(manifestPath, parsedBaseURL, provider); err != nil {
		return err
	}
	return nil
}

func parseGoogleWorkspaceBaseURL(baseURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse google-workspace base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("google-workspace base URL must include scheme and host, got %q", baseURL)
	}
	return parsed, nil
}

func rewriteGoogleWorkspaceTool(toolPath string, baseURL *url.URL) error {
	toolRaw, err := os.ReadFile(toolPath)
	if err != nil {
		return fmt.Errorf("read google-workspace tool: %w", err)
	}
	trimmedBase := strings.TrimRight(baseURL.String(), "/")
	rewrittenEndpoint := trimmedBase + googleWorkspaceEndpointPath
	replacement := fmt.Sprintf(`const USERS_ENDPOINT = %q;`, rewrittenEndpoint)
	updatedTool := googleWorkspaceEndpointRe.ReplaceAllString(string(toolRaw), replacement)
	if updatedTool == string(toolRaw) {
		return fmt.Errorf("google-workspace tool did not contain USERS_ENDPOINT constant")
	}
	if !strings.Contains(string(toolRaw), googleWorkspaceDefaultEndpoint) {
		return fmt.Errorf("google-workspace tool did not contain expected default endpoint %q", googleWorkspaceDefaultEndpoint)
	}
	if err := os.WriteFile(toolPath, []byte(updatedTool), 0o644); err != nil {
		return fmt.Errorf("write google-workspace tool: %w", err)
	}
	return nil
}

func rewriteGoogleWorkspaceManifest(manifestPath string, baseURL *url.URL, provider tooldef.OAuth2ProviderRef) error {
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read google-workspace manifest: %w", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return fmt.Errorf("decode google-workspace manifest: %w", err)
	}

	hostname := baseURL.Hostname()
	if strings.TrimSpace(hostname) == "" {
		return fmt.Errorf("google-workspace base URL %q resolved to empty hostname", baseURL.String())
	}
	manifest["allowed_hosts"] = []string{hostname}

	credentialsRaw, hasCredentials := manifest["credentials"]
	if hasCredentials {
		credentials, ok := credentialsRaw.([]any)
		if !ok || len(credentials) != 1 {
			return fmt.Errorf("google-workspace manifest credentials malformed: %#v", credentialsRaw)
		}
		credential, ok := credentials[0].(map[string]any)
		if !ok {
			return fmt.Errorf("google-workspace credential malformed: %#v", credentials[0])
		}
		inject, ok := credential["inject"].(map[string]any)
		if !ok {
			return fmt.Errorf("google-workspace credential inject malformed: %#v", credential["inject"])
		}
		inject["hosts"] = []string{hostname}

		if !provider.IsZero() {
			if _, err := tooldef.ResolveOAuth2Provider(provider); err != nil {
				return fmt.Errorf("google-workspace provider rewrite invalid: %w", err)
			}
			providerJSON, err := json.Marshal(provider)
			if err != nil {
				return fmt.Errorf("encode google-workspace provider: %w", err)
			}
			var providerValue any
			if err := json.Unmarshal(providerJSON, &providerValue); err != nil {
				return fmt.Errorf("decode google-workspace provider rewrite: %w", err)
			}
			credential["provider"] = providerValue
		}
	}

	updatedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode google-workspace manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(updatedManifest, '\n'), 0o644); err != nil {
		return fmt.Errorf("write google-workspace manifest: %w", err)
	}
	return nil
}

// PrepareGoogleWorkspaceFixture copies the fixture and rewrites it to target
// the provided base URL.
func PrepareGoogleWorkspaceFixture(t testing.TB, baseURL string, provider tooldef.OAuth2ProviderRef) string {
	t.Helper()
	dir := CopyGoogleWorkspaceFixture(t)
	RewriteGoogleWorkspaceFixtureBaseURL(t, dir, baseURL, provider)
	return dir
}

// AssertPreparedGoogleWorkspaceFixtureContract verifies that a copied fixture still
// reflects the package-author contract after local base URL and provider rewrites.
func AssertPreparedGoogleWorkspaceFixtureContract(t testing.TB, dir, baseURL string, provider tooldef.OAuth2ProviderRef) {
	t.Helper()

	parsedBaseURL, err := parseGoogleWorkspaceBaseURL(baseURL)
	if err != nil {
		t.Fatalf("parse google-workspace base URL: %v", err)
	}

	toolRaw, err := os.ReadFile(filepath.Join(dir, "tools", "users.list.ts"))
	if err != nil {
		t.Fatalf("read google-workspace tool: %v", err)
	}
	wantEndpoint := strings.TrimRight(parsedBaseURL.String(), "/") + googleWorkspaceEndpointPath
	if !strings.Contains(string(toolRaw), fmt.Sprintf(`const USERS_ENDPOINT = %q;`, wantEndpoint)) {
		t.Fatalf("rewritten tool missing endpoint %q:\n%s", wantEndpoint, string(toolRaw))
	}

	manifestRaw, err := os.ReadFile(filepath.Join(dir, "toolbox.devpkg.json"))
	if err != nil {
		t.Fatalf("read google-workspace manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("decode google-workspace manifest: %v", err)
	}

	if got, ok := manifest["module"].(string); got != string(googleWorkspaceModulePath) || !ok {
		t.Fatalf("manifest module = %#v, want %q", manifest["module"], googleWorkspaceModulePath)
	}
	gotAllowedHosts, ok := manifest["allowed_hosts"].([]any)
	if !ok {
		t.Fatalf("allowed_hosts = %#v, want []any", manifest["allowed_hosts"])
	}
	if diff := cmp.Diff([]any{parsedBaseURL.Hostname()}, gotAllowedHosts); diff != "" {
		t.Fatalf("allowed_hosts mismatch (-want +got):\n%s", diff)
	}

	credentials, ok := manifest["credentials"].([]any)
	if !ok || len(credentials) != 1 {
		t.Fatalf("manifest credentials = %#v, want single oauth credential", manifest["credentials"])
	}
	credential, ok := credentials[0].(map[string]any)
	if !ok {
		t.Fatalf("manifest credential malformed: %#v", credentials[0])
	}
	if got := credential["name"]; got != googleWorkspaceCredentialName {
		t.Fatalf("credential name = %#v, want %q", got, googleWorkspaceCredentialName)
	}
	if got := credential["type"]; got != string(tooldef.CredentialTypeOAuth2) {
		t.Fatalf("credential type = %#v, want oauth2", got)
	}
	inject, ok := credential["inject"].(map[string]any)
	if !ok {
		t.Fatalf("credential inject malformed: %#v", credential["inject"])
	}
	gotInjectHosts, ok := inject["hosts"].([]any)
	if !ok {
		t.Fatalf("inject.hosts = %#v, want []any", inject["hosts"])
	}
	if diff := cmp.Diff([]any{parsedBaseURL.Hostname()}, gotInjectHosts); diff != "" {
		t.Fatalf("inject.hosts mismatch (-want +got):\n%s", diff)
	}
	if got := inject["method"]; got != "bearer_header" {
		t.Fatalf("inject.method = %#v, want bearer_header", got)
	}

	if !provider.IsZero() {
		providerJSON, err := json.Marshal(provider)
		if err != nil {
			t.Fatalf("encode google-workspace provider: %v", err)
		}
		var wantProvider any
		if err := json.Unmarshal(providerJSON, &wantProvider); err != nil {
			t.Fatalf("decode google-workspace provider: %v", err)
		}
		if diff := cmp.Diff(wantProvider, credential["provider"]); diff != "" {
			t.Fatalf("provider mismatch (-want +got):\n%s", diff)
		}
	}

	resolved, err := GoogleWorkspaceBuilderFromDir(t, dir).Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("Resolve(toolset.Config{}): %v", err)
	}
	AssertGoogleWorkspaceAgentViewHidden(t, resolved)
}

// NewGoogleAuthHarness prepares a copied google-workspace fixture, a real local
// secret store, and the Google emulate provider endpoints for end-to-end auth tests.
func NewGoogleAuthHarness(t *testing.T) *GoogleAuthHarness {
	t.Helper()

	srv := emulatetest.StartGoogle(t)
	provider, err := srv.GoogleProviderRef(context.Background())
	if err != nil {
		t.Fatalf("GoogleProviderRef(): %v", err)
	}
	identityPath := writeAgeIdentityFile(t, t.TempDir())
	storePath := filepath.Join(t.TempDir(), "secrets.age")
	runtimeCAPath := writeRuntimeCertFile(t, t.TempDir(), srv.SecureProxyCertificatePEM())
	return &GoogleAuthHarness{
		Server:        srv,
		Provider:      provider,
		FixtureDir:    PrepareGoogleWorkspaceFixture(t, srv.AuthBaseURL(), provider),
		StorePath:     storePath,
		IdentityPath:  identityPath,
		RuntimeCAPath: runtimeCAPath,
		Store:         secrets.NewLocalSecretStore(storePath, identityPath),
	}
}

func writeAgeIdentityFile(t testing.TB, dir string) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity(): %v", err)
	}
	path := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(path, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

func writeRuntimeCertFile(t testing.TB, dir string, certPEM []byte) string {
	t.Helper()
	if len(certPEM) == 0 {
		t.Fatal("runtime cert PEM is required")
	}
	path := filepath.Join(dir, "emulate-root.pem")
	if err := os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

// UseRuntimeTLSRoots configures the process to trust the emulate HTTPS façade for runtime refreshes.
func (h *GoogleAuthHarness) UseRuntimeTLSRoots(t testing.TB) {
	t.Helper()
	if h == nil || strings.TrimSpace(h.RuntimeCAPath) == "" {
		t.Fatal("google auth harness runtime CA path is required")
	}
	t.Setenv("SSL_CERT_FILE", h.RuntimeCAPath)
}

// GoogleWorkspaceOAuthSecretFamily returns the package-scope durable secret namespace used by
// the google-workspace fixture's OAuth credential.
func GoogleWorkspaceOAuthSecretFamily(t testing.TB) string {
	t.Helper()
	return GoogleWorkspaceOAuthSecretFamilyForTenant(t, "")
}

// GoogleWorkspaceOAuthSecretFamilyForTenant returns the durable secret namespace used by
// the google-workspace fixture's OAuth credential for the given tenant scope.
func GoogleWorkspaceOAuthSecretFamilyForTenant(t testing.TB, tenant string) string {
	t.Helper()
	family, err := tooldef.CredentialFamilyNamespace(googleWorkspaceModulePath, tenant, googleWorkspaceCredentialName)
	if err != nil {
		t.Fatalf("google-workspace oauth secret family: %v", err)
	}
	return family
}

// GoogleWorkspaceOAuthSecretKey returns one package-scope durable secret key under the
// google-workspace fixture's OAuth credential namespace.
func GoogleWorkspaceOAuthSecretKey(t testing.TB, member string) string {
	t.Helper()
	return GoogleWorkspaceOAuthSecretKeyForTenant(t, "", member)
}

// GoogleWorkspaceOAuthSecretKeyForTenant returns one durable secret key under the
// google-workspace fixture's OAuth credential namespace for the given tenant scope.
func GoogleWorkspaceOAuthSecretKeyForTenant(t testing.TB, tenant, member string) string {
	t.Helper()
	key, err := tooldef.CredentialFamilyMemberKey(GoogleWorkspaceOAuthSecretFamilyForTenant(t, tenant), member)
	if err != nil {
		t.Fatalf("google-workspace oauth secret key %q: %v", member, err)
	}
	return key
}

// AssertDurableOAuthState verifies the persisted google-workspace durable secret family shape.
func (h *GoogleAuthHarness) AssertDurableOAuthState(t testing.TB, tenant, clientID, clientSecret string) GoogleDurableOAuthState {
	t.Helper()
	if h == nil || h.Store == nil {
		t.Fatal("google auth harness store is required")
	}
	ctx := context.Background()
	namespace := GoogleWorkspaceOAuthSecretFamilyForTenant(t, tenant)
	gotKeys, err := h.Store.List(ctx, namespace)
	if err != nil {
		t.Fatalf("List(%q): %v", namespace, err)
	}
	wantKeys := []string{
		GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_id"),
		GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "refresh_token"),
	}
	if strings.TrimSpace(clientSecret) != "" {
		wantKeys = append(wantKeys, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_secret"))
	}
	sort.Strings(wantKeys)
	if diff := cmp.Diff(wantKeys, gotKeys); diff != "" {
		t.Fatalf("durable secret keys mismatch for %q (-want +got):\n%s", namespace, diff)
	}
	assertLocalStoreValue(t, h.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_id"), clientID)
	refreshToken := assertLocalStoreNonEmpty(t, h.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "refresh_token"))
	if !strings.HasPrefix(refreshToken, "google_refresh_") {
		t.Fatalf("refresh_token = %q, want emulate google refresh token prefix", refreshToken)
	}
	if strings.TrimSpace(clientSecret) == "" {
		assertLocalStoreMissing(t, h.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_secret"))
	} else {
		assertLocalStoreValue(t, h.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "client_secret"), clientSecret)
	}
	assertLocalStoreMissing(t, h.Store, GoogleWorkspaceOAuthSecretKeyForTenant(t, tenant, "access_token"))
	return GoogleDurableOAuthState{
		Namespace:     namespace,
		PersistedKeys: gotKeys,
		RefreshToken:  refreshToken,
	}
}

func assertLocalStoreValue(t testing.TB, store *secrets.LocalSecretStore, key string, want string) {
	t.Helper()
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, string(got), want)
	}
}

func assertLocalStoreNonEmpty(t testing.TB, store *secrets.LocalSecretStore, key string) string {
	t.Helper()
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if strings.TrimSpace(string(got)) == "" {
		t.Fatalf("Get(%q) returned empty value", key)
	}
	return string(got)
}

func assertLocalStoreMissing(t testing.TB, store *secrets.LocalSecretStore, key string) {
	t.Helper()
	_, err := store.Get(context.Background(), key)
	if err == nil {
		t.Fatalf("Get(%q) unexpectedly succeeded", key)
	}
	if err != secrets.ErrNotFound {
		t.Fatalf("Get(%q) error = %v, want ErrNotFound", key, err)
	}
}

// AssertGoogleWorkspaceAgentViewHidden verifies the agent-visible schema stays free of credential-shaped inputs.
func AssertGoogleWorkspaceAgentViewHidden(t testing.TB, resolved toolset.ResolvedToolset) {
	t.Helper()
	view := resolved.AgentView()
	if len(view.Tools) != 1 {
		t.Fatalf("agent view tool count = %d, want 1", len(view.Tools))
	}
	if view.Tools[0].Name != "users.list" {
		t.Fatalf("agent view tool name = %q, want users.list", view.Tools[0].Name)
	}
	propsAny, hasProps := view.Tools[0].ParamsSchema["properties"]
	if !hasProps || propsAny == nil {
		return
	}
	props, ok := propsAny.(map[string]any)
	if !ok {
		t.Fatalf("params schema properties malformed: %#v", view.Tools[0].ParamsSchema)
	}
	if len(props) != 0 {
		t.Fatalf("users.list params schema properties = %#v, want none", props)
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "workspace", "client_secret"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("credential-shaped param %q should not appear in AgentView schema", forbidden)
		}
	}
}

// GoogleWorkspaceBuilder returns a *toolset.Builder loaded with the
// google-workspace fixture package.
func GoogleWorkspaceBuilder(t testing.TB) *toolset.Builder {
	t.Helper()
	return GoogleWorkspaceBuilderFromDir(t, googleWorkspaceFixtureDir())
}

// GoogleWorkspaceBuilderFromDir returns a *toolset.Builder loaded from the
// given google-workspace fixture directory.
func GoogleWorkspaceBuilderFromDir(t testing.TB, dir string) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(dir); err != nil {
		t.Fatalf("add google-workspace package dir: %v", err)
	}
	return builder
}
