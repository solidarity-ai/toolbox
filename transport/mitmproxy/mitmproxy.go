// Package mitmproxy provides an HTTPS man-in-the-middle proxy that terminates
// TLS connections using dynamically generated per-host certificates signed by
// an embedded CA. This allows the proxy to inspect and log plaintext HTTP
// traffic for audit and mediation purposes.
//
// The embedded CA cert can be injected into guest runtimes via SSL_CERT_FILE,
// and the proxy address via HTTPS_PROXY, so that standard HTTP clients route
// through the proxy transparently.
package mitmproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/solidarity-ai/toolbox/fetch"
	"github.com/solidarity-ai/toolbox/transport"
)

//go:embed ca_cert.pem
var caCertPEM []byte

//go:embed ca_key.pem
var caKeyPEM []byte

// CACertPEM returns the PEM-encoded CA certificate. Callers can write this
// to a file and point SSL_CERT_FILE at it so that clients trust the proxy.
func CACertPEM() []byte {
	return caCertPEM
}

// Proxy is an HTTPS MITM proxy. It listens as a plain HTTP server, handles
// CONNECT requests by generating per-host TLS certificates on the fly, and
// forwards traffic to the real upstream while exposing the plaintext to an
// optional Observer.
type Proxy struct {
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey

	certCache   map[string]*tls.Certificate
	certCacheMu sync.Mutex

	// Observer is called with each intercepted request/response pair.
	// If nil, traffic is forwarded silently.
	Observer Observer

	// Policy is the shared request-preflight seam used to prepare proxied
	// requests before any upstream dial or write occurs.
	Policy *transport.Policy

	// UpstreamTLSConfig is the TLS configuration used when connecting to
	// upstream servers. If nil, the default system trust store is used.
	UpstreamTLSConfig *tls.Config

	// UpstreamDialTLS, when set, overrides how the proxy establishes the TLS
	// upstream connection. Tests can use this to remap dial targets while still
	// exercising the real proxy preflight and rewrite path.
	UpstreamDialTLS func(network, addr string, cfg *tls.Config) (net.Conn, error)
}

// Observer receives intercepted HTTP request/response pairs.
type Observer interface {
	Observe(host string, req *http.Request, resp *http.Response)
}

// ObserverFunc adapts a plain function to the Observer interface.
type ObserverFunc func(host string, req *http.Request, resp *http.Response)

func (f ObserverFunc) Observe(host string, req *http.Request, resp *http.Response) {
	f(host, req, resp)
}

// New creates a new Proxy using the embedded CA certificate and key.
func New(observer Observer) (*Proxy, error) {
	certBlock, _ := pem.Decode(caCertPEM)
	if certBlock == nil {
		return nil, errors.New("mitmproxy: failed to decode embedded CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}

	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, errors.New("mitmproxy: failed to decode embedded CA key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}

	return &Proxy{
		caCert:    caCert,
		caKey:     caKey,
		certCache: make(map[string]*tls.Certificate),
		Observer:  observer,
	}, nil
}

// ListenAndServe starts the proxy on a random available port and returns the
// listener. The caller can use Addr().String() to get the host:port.
func (p *Proxy) ListenAndServe() (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	server := &http.Server{Handler: p}
	go server.Serve(listener)
	return listener, nil
}

// ServeHTTP implements http.Handler, accepting only CONNECT requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	http.Error(w, "only CONNECT is supported", http.StatusMethodNotAllowed)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	targetHost := r.Host
	host, _, err := net.SplitHostPort(targetHost)
	if err != nil {
		host = targetHost
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	hostCert, err := p.getOrCreateCert(host)
	if err != nil {
		return
	}

	tlsClientConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*hostCert},
	})
	if err := tlsClientConn.Handshake(); err != nil {
		return
	}
	defer tlsClientConn.Close()

	clientReader := bufio.NewReader(tlsClientConn)

	var (
		upstreamConn   *tls.Conn
		upstreamReader *bufio.Reader
		upstreamTarget string
	)
	defer func() {
		if upstreamConn != nil {
			upstreamConn.Close()
		}
	}()

	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		rawURL, err := reconstructHTTPSURL(targetHost, req)
		if err != nil {
			writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy rejected request: %v", err))
			continue
		}

		reqHeaders := fetch.NewHeadersFromHTTP(req.Header)
		preparedURL := rawURL
		if p.Policy != nil {
			preparedURL, err = p.Policy.PrepareRequest(context.Background(), rawURL, reqHeaders)
			if err != nil {
				status := http.StatusBadGateway
				if strings.Contains(err.Error(), "transport denied request") {
					status = http.StatusForbidden
				}
				writeProxyFailure(tlsClientConn, status, err.Error())
				continue
			}
		}

		dialTarget, err := rewritePreparedRequest(req, preparedURL, reqHeaders)
		if err != nil {
			writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy failed to rewrite request: %v", err))
			continue
		}

		if upstreamConn == nil {
			upstreamTLS := cloneTLSConfig(p.UpstreamTLSConfig)
			if upstreamTLS.ServerName == "" {
				serverName := req.URL.Hostname()
				if serverName == "" {
					serverName = host
				}
				upstreamTLS.ServerName = serverName
			}
			var rawUpstreamConn net.Conn
			if p.UpstreamDialTLS != nil {
				rawUpstreamConn, err = p.UpstreamDialTLS("tcp", dialTarget, upstreamTLS)
			} else {
				rawUpstreamConn, err = tls.Dial("tcp", dialTarget, upstreamTLS)
			}
			if err != nil {
				writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy failed to connect upstream: %v", err))
				upstreamConn = nil
				continue
			}
			var ok bool
			upstreamConn, ok = rawUpstreamConn.(*tls.Conn)
			if !ok {
				writeProxyFailure(tlsClientConn, http.StatusBadGateway, "proxy failed to connect upstream: dialer returned non-TLS connection")
				_ = rawUpstreamConn.Close()
				continue
			}
			upstreamReader = bufio.NewReader(upstreamConn)
			upstreamTarget = dialTarget
		} else if dialTarget != upstreamTarget {
			writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy target mismatch: tunnel established for %s but request resolved to %s", upstreamTarget, dialTarget))
			continue
		}

		if err := req.Write(upstreamConn); err != nil {
			writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy failed to forward request: %v", err))
			return
		}

		resp, err := http.ReadResponse(upstreamReader, req)
		if err != nil {
			writeProxyFailure(tlsClientConn, http.StatusBadGateway, fmt.Sprintf("proxy failed to read upstream response: %v", err))
			return
		}

		if p.Observer != nil {
			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return
			}
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			obsResp := *resp
			obsResp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			p.Observer.Observe(host, req, &obsResp)
		}

		if err := resp.Write(tlsClientConn); err != nil {
			return
		}
	}
}

func reconstructHTTPSURL(connectTarget string, req *http.Request) (string, error) {
	if req == nil || req.URL == nil {
		return "", fmt.Errorf("missing request URL")
	}

	authority := req.URL.Host
	if authority == "" {
		authority = req.Host
	}
	if authority == "" {
		authority = connectTarget
	}
	if authority == "" {
		return "", fmt.Errorf("missing CONNECT target host")
	}

	parsed := *req.URL
	parsed.Scheme = "https"
	parsed.Host = authority
	if parsed.Path == "" && parsed.Opaque == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func rewritePreparedRequest(req *http.Request, preparedURL string, headers *fetch.Headers) (string, error) {
	if req == nil {
		return "", fmt.Errorf("missing request")
	}
	parsed, err := url.Parse(preparedURL)
	if err != nil {
		return "", fmt.Errorf("parse prepared url: %w", err)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("parse prepared url: missing host")
	}

	req.URL = parsed
	req.Host = parsed.Host
	req.RequestURI = ""
	req.Header = make(http.Header)
	for _, entry := range headers.Entries() {
		req.Header.Add(entry[0], entry[1])
	}

	return authorityForHTTPS(parsed), nil
}

func authorityForHTTPS(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	if parsed.Port() != "" {
		return parsed.Host
	}
	return net.JoinHostPort(parsed.Hostname(), "443")
}

func writeProxyFailure(w io.Writer, status int, message string) {
	if message == "" {
		message = http.StatusText(status)
	}
	resp := &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(message + "\n")),
		ContentLength: int64(len(message) + 1),
		Close:         false,
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	_ = resp.Write(w)
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return cfg.Clone()
}

func (p *Proxy) getOrCreateCert(host string) (*tls.Certificate, error) {
	p.certCacheMu.Lock()
	defer p.certCacheMu.Unlock()

	if cert, ok := p.certCache[host]; ok {
		return cert, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
	p.certCache[host] = tlsCert
	return tlsCert, nil
}
