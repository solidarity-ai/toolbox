package mcpserver_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/credentialrepo"
	"github.com/solidarity-ai/toolbox/credpath"
	"github.com/solidarity-ai/toolbox/mcpserver"
	"github.com/solidarity-ai/toolbox/testutil"
	"github.com/solidarity-ai/toolbox/testutil/mcptest"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/transport"
)

// headerCapture is a concurrency-safe string capture using atomic.Value.
// It stores and loads a single string value, typically used to record
// HTTP headers or URLs received by a test server.
type headerCapture struct {
	v atomic.Value
}

var defaultTransportMu sync.Mutex

func newHeaderCapture() *headerCapture {
	h := &headerCapture{}
	h.v.Store("")
	return h
}

func (h *headerCapture) Store(s string) { h.v.Store(s) }
func (h *headerCapture) Load() string   { return h.v.Load().(string) }

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type connChanListener struct {
	addr      net.Addr
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newConnChanListener(addr net.Addr) *connChanListener {
	return &connChanListener{
		addr:   addr,
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *connChanListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn := <-l.conns:
		if conn == nil {
			return nil, net.ErrClosed
		}
		return conn, nil
	}
}

func (l *connChanListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *connChanListener) Addr() net.Addr { return l.addr }

type sameHostDualSchemeServer struct {
	addr      string
	certPEM   []byte
	raw       net.Listener
	plain     *http.Server
	tls       *http.Server
	plainLn   *connChanListener
	tlsLn     *connChanListener
	routeDone chan struct{}
	closeOnce sync.Once
}

func newSameHostDualSchemeServer(t *testing.T, handler http.Handler) *sameHostDualSchemeServer {
	t.Helper()

	certPEM, keyPEM := selfSignedCert(t, "127.0.0.1")
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	plainLn := newConnChanListener(raw.Addr())
	tlsLn := newConnChanListener(raw.Addr())
	routeDone := make(chan struct{})

	srv := &sameHostDualSchemeServer{
		addr:      raw.Addr().String(),
		certPEM:   certPEM,
		raw:       raw,
		plain:     &http.Server{Handler: handler},
		tls:       &http.Server{Handler: handler},
		plainLn:   plainLn,
		tlsLn:     tlsLn,
		routeDone: routeDone,
	}

	go func() {
		_ = srv.plain.Serve(plainLn)
	}()
	go func() {
		_ = srv.tls.Serve(tls.NewListener(tlsLn, &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}))
	}()
	go func() {
		defer close(routeDone)
		for {
			conn, err := raw.Accept()
			if err != nil {
				return
			}
			go srv.routeConn(conn)
		}
	}()

	t.Cleanup(func() {
		srv.Close()
	})

	return srv
}

func (s *sameHostDualSchemeServer) routeConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	firstByte, err := reader.Peek(1)
	if err != nil {
		_ = conn.Close()
		return
	}

	target := s.plainLn.conns
	if len(firstByte) > 0 && firstByte[0] == 0x16 {
		target = s.tlsLn.conns
	}

	select {
	case <-s.routeDone:
		_ = conn.Close()
	case target <- &bufferedConn{Conn: conn, reader: reader}:
	}
}

func (s *sameHostDualSchemeServer) URL(scheme, path string) string {
	return scheme + "://" + s.addr + path
}

func (s *sameHostDualSchemeServer) Close() {
	s.closeOnce.Do(func() {
		_ = s.raw.Close()
		_ = s.plainLn.Close()
		_ = s.tlsLn.Close()
		_ = s.plain.Close()
		_ = s.tls.Close()
		<-s.routeDone
	})
}

func trustCertInDefaultTransport(t *testing.T, certPEM []byte) {
	t.Helper()

	defaultTransportMu.Lock()

	previous := http.DefaultTransport
	transport, ok := previous.(*http.Transport)
	if !ok {
		defaultTransportMu.Unlock()
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", previous)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(certPEM) {
		defaultTransportMu.Unlock()
		t.Fatal("failed to add test cert to root pool")
	}

	cloned := transport.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{}
	} else {
		cloned.TLSClientConfig = cloned.TLSClientConfig.Clone()
	}
	cloned.TLSClientConfig.RootCAs = pool
	http.DefaultTransport = cloned

	t.Cleanup(func() {
		http.DefaultTransport = previous
		defaultTransportMu.Unlock()
	})
}

func selfSignedCert(t *testing.T, host string) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

// newTokenServer starts an httptest.Server that always returns the given
// access token with token_type "Bearer" and expires_in 3600.
func newTokenServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTokenServerWithRefreshMap starts a token server that maps refresh tokens
// to access tokens. Unknown refresh tokens receive 401.
func newTokenServerWithRefreshMap(t *testing.T, refreshToAccess map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		accessToken, ok := refreshToAccess[r.FormValue("refresh_token")]
		if !ok {
			http.Error(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// seedOAuth2 populates the store with OAuth2 shared client creds and a
// per-account refresh token using credpath helpers.
func seedOAuth2(store *testutil.TestSecretStore, module, cred, account, clientID, clientSecret, refreshToken string) {
	store.Seed(map[string][]byte{
		credpath.OAuth2ClientID(module, cred):              []byte(clientID),
		credpath.OAuth2ClientSecret(module, cred):          []byte(clientSecret),
		credpath.OAuth2RefreshToken(module, cred, account): []byte(refreshToken),
	})
}

// seedAPIKey populates the store with an API key under a specific account.
func seedAPIKey(store *testutil.TestSecretStore, module, cred, account, apiKey string) {
	store.Seed(map[string][]byte{
		credpath.APIKey(module, cred, account): []byte(apiKey),
	})
}

// newFetchTestHarness prepares the fetch-test fixture toolset with the given
// config, creates an MCP server and harness, and returns the harness.
func newFetchTestHarness(t *testing.T, policy toolset.PackageCredentialPolicy) *mcptest.Harness {
	t.Helper()
	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("fetch-test"), singlePackageConfig("fixtures.local/fetch-test", policy))
	srv := mcpserver.New(prepared)
	return mcptest.NewHarness(t, srv)
}

// newAuthTestHarness loads the auth-test fixture, builds injection rules,
// creates a toolset with the given config, and returns the harness.
// The caller is responsible for seeding store and setting up credAccounts before
// calling this; the returned harness is ready for CallTool.
func newAuthTestHarness(t *testing.T, policy toolset.PackageCredentialPolicy) *mcptest.Harness {
	t.Helper()
	prepared := tooltest.PrepareToolset(t, tooltest.DistPackageDecl("auth-test"), singlePackageConfig("fixtures.local/auth-test", policy))
	srv := mcpserver.New(prepared)
	return mcptest.NewHarness(t, srv)
}

func singlePackageConfig(module tooldef.ModulePath, policy toolset.PackageCredentialPolicy) toolset.Config {
	return toolset.Config{
		CredentialPolicySource: credentialrepo.StaticPolicySource{
			module: policy,
		},
	}
}

// newOAuth2Rule creates an OAuth2 bearer injection rule for the given host and
// secret prefix with the specified token URL.
func newOAuth2Rule(host, module, cred, secretPrefix, tokenURL string) transport.InjectionRule {
	return transport.InjectionRule{
		Hosts:                    []string{host},
		ModuleName:               module,
		CredentialName:           cred,
		SecretPrefix:             secretPrefix,
		Type:                     transport.CredentialTypeOAuth2,
		Method:                   transport.InjectionMethodBearerHeader,
		AllowUnsafeHTTPInjection: true,
		Provider:                 &transport.OAuth2Provider{TokenURL: tokenURL},
	}
}

// newAPIKeyHeaderRule creates an API key header injection rule.
// If headerName is empty, the default X-API-Key header is used.
func newAPIKeyHeaderRule(host, module, cred, secretPrefix, headerName string) transport.InjectionRule {
	return transport.InjectionRule{
		Hosts:                    []string{host},
		ModuleName:               module,
		CredentialName:           cred,
		SecretPrefix:             secretPrefix,
		Type:                     transport.CredentialTypeAPIKey,
		Method:                   transport.InjectionMethodAPIKeyHeader,
		HeaderName:               headerName,
		AllowUnsafeHTTPInjection: true,
	}
}
