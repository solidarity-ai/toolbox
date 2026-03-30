package tooltest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	HTTPClientToolName = "httpClient.fetch"
	HTTPClientModule   = tool.ModulePath("github.com/example/http-client")
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

func ResolveHTTPClientToolset(t testing.TB, secretStore secrets.SecretStore) toolset.ResolvedToolset {
	t.Helper()
	builder := toolset.New()
	if err := builder.AddFromDir(HTTPClientFixtureDir(t)); err != nil {
		t.Fatalf("add http-client package dir: %v", err)
	}
	resolved, err := builder.Resolve(toolset.Config{SecretStore: secretStore})
	if err != nil {
		t.Fatalf("resolve http-client toolset: %v", err)
	}
	return resolved
}

func HTTPClientSecretKey(t testing.TB, name string) string {
	t.Helper()
	key, err := tool.CredentialSecretKey(HTTPClientModule, name)
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
