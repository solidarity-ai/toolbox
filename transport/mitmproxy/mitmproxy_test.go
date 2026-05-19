package mitmproxy_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestMITMProxy(t *testing.T) {
	// Start a local HTTPS upstream.
	upstreamCert, upstreamKey := selfSignedCert(t, "127.0.0.1")
	upstream, upstreamAddr := startUpstream(t, upstreamCert, upstreamKey)
	defer upstream.Close()

	// Build an upstream trust pool so the proxy trusts our test server.
	upstreamPool := x509.NewCertPool()
	upstreamPool.AppendCertsFromPEM(upstreamCert)

	// Track what the proxy observes.
	var (
		mu          sync.Mutex
		observedURL string
		observedHdr string
	)

	proxy, err := mitmproxy.New(mitmproxy.ObserverFunc(func(host string, req *http.Request, resp *http.Response) {
		mu.Lock()
		defer mu.Unlock()
		observedURL = req.URL.String()
		observedHdr = resp.Header.Get("X-Test-Secret")
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy.UpstreamTLSConfig = &tls.Config{RootCAs: upstreamPool}

	proxyListener, err := proxy.ListenAndServe()
	if err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	defer proxyListener.Close()

	// Build a client that trusts the proxy's CA.
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(mitmproxy.CACertPEM()) {
		t.Fatal("failed to add CA cert")
	}
	proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}

	resp, err := client.Get("https://" + upstreamAddr + "/get")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["message"] != "hello from upstream" {
		t.Errorf("body = %v, want message=hello from upstream", body)
	}

	mu.Lock()
	defer mu.Unlock()
	if observedURL != "/get" {
		t.Errorf("observer URL = %q, want /get", observedURL)
	}
	if observedHdr != "visible-to-proxy" {
		t.Errorf("observer header = %q, want visible-to-proxy", observedHdr)
	}
}

func startUpstream(t *testing.T, certPEM, keyPEM []byte) (*http.Server, string) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Test-Secret", "visible-to-proxy")
		json.NewEncoder(w).Encode(map[string]string{"message": "hello from upstream"})
	})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})

	server := &http.Server{Handler: mux}
	go server.Serve(tlsListener)
	return server, listener.Addr().String()
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

func TestMITMProxyLargeBody(t *testing.T) {
	upstreamCert, upstreamKey := selfSignedCert(t, "127.0.0.1")
	
	const largeSize = 11 << 20 // 11 MB
	mux := http.NewServeMux()
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 1024)
		for i := range chunk {
			chunk[i] = 'a'
		}
		for i := 0; i < largeSize/len(chunk); i++ {
			_, _ = w.Write(chunk)
		}
	})

	tlsCert, err := tls.X509KeyPair(upstreamCert, upstreamKey)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})
	server := &http.Server{Handler: mux}
	go server.Serve(tlsListener)
	defer server.Close()

	upstreamAddr := listener.Addr().String()

	upstreamPool := x509.NewCertPool()
	upstreamPool.AppendCertsFromPEM(upstreamCert)

	var (
		mu          sync.Mutex
		observedLen int
	)

	proxy, err := mitmproxy.New(mitmproxy.ObserverFunc(func(host string, req *http.Request, resp *http.Response) {
		mu.Lock()
		defer mu.Unlock()
		bodyBytes, _ := io.ReadAll(resp.Body)
		observedLen = len(bodyBytes)
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy.UpstreamTLSConfig = &tls.Config{RootCAs: upstreamPool}

	proxyListener, err := proxy.ListenAndServe()
	if err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	defer proxyListener.Close()

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(mitmproxy.CACertPEM()) {
		t.Fatal("failed to add CA cert")
	}
	proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}

	resp, err := client.Get("https://" + upstreamAddr + "/large")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	fullBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if len(fullBody) != largeSize {
		t.Errorf("got client body size %d, want %d", len(fullBody), largeSize)
	}

	mu.Lock()
	defer mu.Unlock()
	const maxObserveBody = 10 << 20
	if observedLen != maxObserveBody {
		t.Errorf("got observed body size %d, want %d", observedLen, maxObserveBody)
	}
}

