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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

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

	// Injector, if non-nil, injects credentials into outbound requests
	// before they are forwarded to the upstream server.
	Injector *transport.CredentialInjector

	// Allowlist, if non-nil, restricts which hosts the proxy will connect to.
	Allowlist *transport.HostAllowlist

	// UpstreamTLSConfig is the TLS configuration used when connecting to
	// upstream servers. If nil, the default system trust store is used.
	UpstreamTLSConfig *tls.Config
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

	if p.Allowlist != nil && !p.Allowlist.Allows(host) {
		http.Error(w, "host not in allowlist", http.StatusForbidden)
		return
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

	upstreamTLS := p.UpstreamTLSConfig
	if upstreamTLS == nil {
		upstreamTLS = &tls.Config{}
	}
	upstreamConn, err := tls.Dial("tcp", targetHost, upstreamTLS)
	if err != nil {
		return
	}
	defer upstreamConn.Close()

	clientReader := bufio.NewReader(tlsClientConn)
	upstreamReader := bufio.NewReader(upstreamConn)

	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}

		// Credential injection — tool code never sees this.
		if p.Injector != nil {
			reqURL := "https://" + host + req.URL.RequestURI()
			var hdrs [][2]string
			for k, vals := range req.Header {
				for _, v := range vals {
					hdrs = append(hdrs, [2]string{k, v})
				}
			}
			newURL, newHdrs, injected, injErr := p.Injector.InjectRequest(req.Method, reqURL, hdrs)
			if injErr != nil {
				// Write error response to client and skip this request.
				errBody := marshalJSON(map[string]string{"error": "credential injection failed"})
				errResp := &http.Response{
					StatusCode: http.StatusBadGateway,
					Status:     "502 Bad Gateway",
					Proto:      "HTTP/1.1",
					ProtoMajor: 1, ProtoMinor: 1,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   io.NopCloser(bytes.NewReader(errBody)),
				}
				// Surface the injection error to the observer so it is auditable.
				if p.Observer != nil {
					obsResp := *errResp
					obsResp.Body = io.NopCloser(bytes.NewReader(errBody))
					p.Observer.Observe(host, req, &obsResp)
				}
				errResp.Write(tlsClientConn)
				continue
			}
			if injected {
				// Rebuild request headers from the injected set.
				req.Header = make(http.Header)
				for _, h := range newHdrs {
					req.Header.Add(h[0], h[1])
				}
				// Update URL if changed (e.g. api_key_query).
				if newURL != reqURL {
					if parsed, parseErr := req.URL.Parse(newURL); parseErr == nil {
						req.URL = parsed
					}
				}
			}
		}

		if err := req.Write(upstreamConn); err != nil {
			return
		}

		resp, err := http.ReadResponse(upstreamReader, req)
		if err != nil {
			return
		}

		if p.Observer != nil {
			const maxObserveBody = 10 << 20 // 10 MB
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxObserveBody))
			resp.Body.Close()
			if err != nil {
				return
			}
			// Replace body so we can still forward it to the client.
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			// Give observer a copy with its own body reader.
			obsResp := *resp
			obsResp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			p.Observer.Observe(host, req, &obsResp)
		}

		if err := resp.Write(tlsClientConn); err != nil {
			return
		}
	}
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

func marshalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
