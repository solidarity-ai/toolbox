package tooltest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/secrets"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	HTTPClientToolName               = "httpClient.fetch"
	HTTPClientModule                 = tooldef.ModulePath("github.com/example/http-client")
	HTTPClientExecutableName         = "http-client"
	HTTPClientOAuthCredentialName    = "workspace"
	HTTPClientExpectedPackageRuntime = tooldef.RuntimeTypeScriptWasip2Sandbox
)

type HTTPClientObservedRequest struct {
	Path   string
	Query  string
	Header http.Header
	Method string
	Host   string
}

type HTTPClientLocalTLSServer struct {
	addr     string
	certPEM  []byte
	hitCount atomic.Int32

	mu       sync.Mutex
	requests []HTTPClientObservedRequest
}

func HTTPClientFixtureDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("tooltest: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "fixtures", "toolbox.pkgs", "http-client")
}

func CopyHTTPClientFixture(t testing.TB) string {
	t.Helper()

	src := HTTPClientFixtureDir(t)
	dst := t.TempDir()
	if err := copyFixtureDir(src, dst); err != nil {
		t.Fatalf("copy http-client fixture: %v", err)
	}
	return dst
}

func copyFixtureDir(src, dst string) error {
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

func HTTPClientBuilderFromDir(t testing.TB, dir string) *toolset.Builder {
	t.Helper()

	builder := toolset.New()
	if err := builder.AddFromDir(dir); err != nil {
		t.Fatalf("add http-client package dir: %v", err)
	}
	return builder
}

func ResolveHTTPClientToolset(t testing.TB, secretStore secrets.SecretStore) toolset.ResolvedToolset {
	t.Helper()
	resolved, err := HTTPClientBuilderFromDir(t, HTTPClientFixtureDir(t)).Resolve(toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("resolve http-client toolset: %v", err)
	}
	return resolved
}

func PrepareOAuthHTTPClientFixture(t testing.TB, baseURL string, provider tooldef.OAuth2ProviderRef) string {
	t.Helper()
	dir := CopyHTTPClientFixture(t)
	RewriteOAuthHTTPClientFixture(t, dir, baseURL, provider)
	return dir
}

func RewriteOAuthHTTPClientFixture(t testing.TB, dir, baseURL string, provider tooldef.OAuth2ProviderRef) {
	t.Helper()
	if err := rewriteOAuthHTTPClientFixture(dir, baseURL, provider); err != nil {
		t.Fatalf("rewrite oauth http-client fixture: %v", err)
	}
}

func rewriteOAuthHTTPClientFixture(dir, baseURL string, provider tooldef.OAuth2ProviderRef) error {
	parsedBaseURL, err := parseOAuthHTTPClientBaseURL(baseURL)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "toolbox.devpkg.json")
	return rewriteOAuthHTTPClientManifest(manifestPath, parsedBaseURL, provider)
}

func parseOAuthHTTPClientBaseURL(baseURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse http-client base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("http-client base URL must include scheme and host, got %q", baseURL)
	}
	return parsed, nil
}

func rewriteOAuthHTTPClientManifest(manifestPath string, baseURL *url.URL, provider tooldef.OAuth2ProviderRef) error {
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read http-client manifest: %w", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return fmt.Errorf("decode http-client manifest: %w", err)
	}

	runtimeValue, _ := manifest["runtime"].(string)
	if runtimeValue != string(HTTPClientExpectedPackageRuntime) {
		return fmt.Errorf("http-client fixture runtime = %q, want %q", runtimeValue, HTTPClientExpectedPackageRuntime)
	}

	hostname := strings.TrimSpace(baseURL.Hostname())
	if hostname == "" {
		return fmt.Errorf("http-client base URL %q resolved to empty hostname", baseURL.String())
	}
	manifest["allowed_hosts"] = []string{hostname}

	executables, ok := manifest["executables"].(map[string]any)
	if !ok {
		return fmt.Errorf("http-client manifest executables malformed: %#v", manifest["executables"])
	}
	if got, _ := executables[HTTPClientExecutableName].(string); strings.TrimSpace(got) == "" {
		return fmt.Errorf("http-client manifest missing executable %q", HTTPClientExecutableName)
	}

	if _, err := tooldef.ResolveOAuth2Provider(provider); err != nil {
		return fmt.Errorf("http-client provider rewrite invalid: %w", err)
	}
	providerJSON, err := json.Marshal(provider)
	if err != nil {
		return fmt.Errorf("encode http-client provider: %w", err)
	}
	var providerValue any
	if err := json.Unmarshal(providerJSON, &providerValue); err != nil {
		return fmt.Errorf("decode http-client provider rewrite: %w", err)
	}

	manifest["credentials"] = []map[string]any{{
		"name":     HTTPClientOAuthCredentialName,
		"type":     string(tooldef.CredentialTypeOAuth2),
		"provider": providerValue,
		"scopes": []string{
			"openid",
			"email",
			"https://www.googleapis.com/auth/cloud-platform.read-only",
		},
		"inject": map[string]any{
			"hosts":  []string{hostname},
			"method": "bearer_header",
		},
	}}

	updatedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode http-client manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(updatedManifest, '\n'), 0o644); err != nil {
		return fmt.Errorf("write http-client manifest: %w", err)
	}
	return nil
}

func HTTPClientOAuthSecretFamily(t testing.TB) string {
	t.Helper()
	family, err := tooldef.CredentialFamilyNamespace(HTTPClientModule, "", HTTPClientOAuthCredentialName)
	if err != nil {
		t.Fatalf("http-client oauth secret family: %v", err)
	}
	return family
}

func HTTPClientOAuthSecretKey(t testing.TB, member string) string {
	t.Helper()
	key, err := tooldef.CredentialFamilyMemberKey(HTTPClientOAuthSecretFamily(t), member)
	if err != nil {
		t.Fatalf("http-client oauth secret key %q: %v", member, err)
	}
	return key
}

func AssertHTTPClientDurableOAuthState(t testing.TB, store secrets.SecretStore, clientID, clientSecret string) []string {
	t.Helper()
	if store == nil {
		t.Fatal("http-client secret store is required")
	}
	ctx := context.Background()
	namespace := HTTPClientOAuthSecretFamily(t)
	gotKeys, err := store.List(ctx, namespace)
	if err != nil {
		t.Fatalf("List(%q): %v", namespace, err)
	}
	wantKeys := []string{
		HTTPClientOAuthSecretKey(t, "client_id"),
		HTTPClientOAuthSecretKey(t, "refresh_token"),
	}
	if strings.TrimSpace(clientSecret) != "" {
		wantKeys = append(wantKeys, HTTPClientOAuthSecretKey(t, "client_secret"))
	}
	sort.Strings(wantKeys)
	if diff := cmp.Diff(wantKeys, gotKeys); diff != "" {
		t.Fatalf("durable secret keys mismatch for %q (-want +got):\n%s", namespace, diff)
	}
	assertSecretStoreValue(t, store, HTTPClientOAuthSecretKey(t, "client_id"), clientID)
	refreshToken := assertSecretStoreNonEmpty(t, store, HTTPClientOAuthSecretKey(t, "refresh_token"))
	if !strings.HasPrefix(refreshToken, "google_refresh_") {
		t.Fatalf("refresh_token = %q, want emulate google refresh token prefix", refreshToken)
	}
	if strings.TrimSpace(clientSecret) == "" {
		assertSecretStoreMissing(t, store, HTTPClientOAuthSecretKey(t, "client_secret"))
	} else {
		assertSecretStoreValue(t, store, HTTPClientOAuthSecretKey(t, "client_secret"), clientSecret)
	}
	assertSecretStoreMissing(t, store, HTTPClientOAuthSecretKey(t, "access_token"))
	return gotKeys
}

func assertSecretStoreValue(t testing.TB, store secrets.SecretStore, key string, want string) {
	t.Helper()
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, string(got), want)
	}
}

func assertSecretStoreNonEmpty(t testing.TB, store secrets.SecretStore, key string) string {
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

func assertSecretStoreMissing(t testing.TB, store secrets.SecretStore, key string) {
	t.Helper()
	_, err := store.Get(context.Background(), key)
	if err == nil {
		t.Fatalf("Get(%q) unexpectedly succeeded", key)
	}
	if err != secrets.ErrNotFound {
		t.Fatalf("Get(%q) error = %v, want ErrNotFound", key, err)
	}
}

func AssertHTTPClientAgentViewHidden(t testing.TB, resolved toolset.ResolvedToolset) {
	t.Helper()
	view := resolved.AgentView()
	if len(view.Tools) != 1 {
		t.Fatalf("agent view tool count = %d, want 1", len(view.Tools))
	}
	if view.Tools[0].Name != HTTPClientToolName {
		t.Fatalf("agent view tool name = %q, want %s", view.Tools[0].Name, HTTPClientToolName)
	}
	props, ok := view.Tools[0].ParamsSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("params schema properties malformed: %#v", view.Tools[0].ParamsSchema)
	}
	if len(props) != 1 {
		t.Fatalf("params schema properties = %#v, want single url property", props)
	}
	if _, ok := props["url"]; !ok {
		t.Fatalf("params schema properties = %#v, want url property", props)
	}
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "workspace", "client_secret", "client_id"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("credential-shaped param %q should not appear in AgentView schema", forbidden)
		}
	}
}

func HTTPClientSecretKey(t testing.TB, name string) string {
	t.Helper()
	key, err := tooldef.CredentialSecretKey(HTTPClientModule, name)
	if err != nil {
		t.Fatalf("credential secret key %q: %v", name, err)
	}
	return key
}

func StartHTTPClientLocalTLSServer(t testing.TB) *HTTPClientLocalTLSServer {
	t.Helper()

	certPEM, keyPEM := selfSignedCertForHosts(t, "127.0.0.1")
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	h := &HTTPClientLocalTLSServer{certPEM: certPEM}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.hitCount.Add(1)
		h.mu.Lock()
		h.requests = append(h.requests, HTTPClientObservedRequest{
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Header: r.Header.Clone(),
			Method: r.Method,
			Host:   r.Host,
		})
		h.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"path": r.URL.Path,
		})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h.addr = listener.Addr().String()

	server := &http.Server{Handler: mux}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{tlsCert}}))
	t.Cleanup(func() { _ = server.Close() })

	return h
}

func (h *HTTPClientLocalTLSServer) URL(path string) string {
	return "https://" + h.addr + path
}

func (h *HTTPClientLocalTLSServer) RootCAs(t testing.TB) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(h.certPEM) {
		t.Fatal("append local TLS cert to pool")
	}
	return pool
}

func (h *HTTPClientLocalTLSServer) HitCount() int {
	return int(h.hitCount.Load())
}

func (h *HTTPClientLocalTLSServer) LastRequest(t testing.TB) HTTPClientObservedRequest {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(h.requests))
	}
	return h.requests[0]
}

func selfSignedCertForHosts(t testing.TB, hosts ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	if len(hosts) == 0 {
		t.Fatal("selfSignedCertForHosts requires at least one host")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal ECDSA key: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Fatal("encode PEM outputs")
	}
	return certPEM, keyPEM
}

func DescribeObservedRequest(req HTTPClientObservedRequest) string {
	return fmt.Sprintf("%s %s?%s host=%s headers=%v", req.Method, req.Path, req.Query, req.Host, req.Header)
}
