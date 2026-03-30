package tooltest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

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

// GoogleWorkspaceOAuthSecretFamily returns the durable secret namespace used by
// the google-workspace fixture's OAuth credential.
func GoogleWorkspaceOAuthSecretFamily(t testing.TB) string {
	t.Helper()
	family, err := tooldef.CredentialFamilyNamespace(googleWorkspaceModulePath, "", googleWorkspaceCredentialName)
	if err != nil {
		t.Fatalf("google-workspace oauth secret family: %v", err)
	}
	return family
}

// GoogleWorkspaceOAuthSecretKey returns one durable secret key under the
// google-workspace fixture's OAuth credential namespace.
func GoogleWorkspaceOAuthSecretKey(t testing.TB, member string) string {
	t.Helper()
	key, err := tooldef.CredentialFamilyMemberKey(GoogleWorkspaceOAuthSecretFamily(t), member)
	if err != nil {
		t.Fatalf("google-workspace oauth secret key %q: %v", member, err)
	}
	return key
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
