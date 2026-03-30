package mitmproxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/secrets"
	"github.com/solidarity-ai/toolbox/testutil"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
)

func TestMITMProxyPolicyParityOAuth2(t *testing.T) {
	t.Parallel()

	t.Run("OAuth2 refresh reuses one cached token across repeated proxy requests", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})

		var tokenCalls atomic.Int32
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if got := values.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type = %q, want refresh_token", got)
			}
			if got := values.Get("client_id"); got != "client-123" {
				t.Fatalf("client_id = %q, want client-123", got)
			}
			if got := values.Get("client_secret"); got != "secret-123" {
				t.Fatalf("client_secret = %q, want secret-123", got)
			}
			if got := values.Get("refresh_token"); got != "refresh-123" {
				t.Fatalf("refresh_token = %q, want refresh-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		}))
		defer tokenServer.Close()

		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"127.0.0.1"},
			rules:        []transport.Rule{oauth2ProxyRule(tokenServer.URL, "127.0.0.1")},
		})

		for attempt := range 2 {
			resp := harness.mustGet(t, "/oauth2")
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				t.Fatalf("attempt %d status = %d, want 200 (body=%q)", attempt+1, resp.StatusCode, string(body))
			}
			resp.Body.Close()
		}

		obs := harness.observations(t)
		if len(obs) != 2 {
			t.Fatalf("observations = %d, want 2", len(obs))
		}
		for idx, exchange := range obs {
			if got := exchange.RequestHeader.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("observation %d authorization = %q, want refreshed bearer token", idx+1, got)
			}
		}
		if got := harness.upstreamHitCount(); got != 2 {
			t.Fatalf("protected upstream hits = %d, want 2", got)
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1 cached refresh", got)
		}
	})

	t.Run("OAuth2 refresh failures return proxy-visible errors before any upstream dial", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{
			"github.com/example/github-issues/github_oauth/client_id":     "client-123",
			"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
			"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
		})

		var tokenCalls atomic.Int32
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			http.Error(w, "upstream denied", http.StatusBadGateway)
		}))
		defer tokenServer.Close()

		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"127.0.0.1"},
			rules:        []transport.Rule{oauth2ProxyRule(tokenServer.URL, "127.0.0.1")},
		})

		resp := harness.mustGet(t, "/oauth2-failure")
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
		if !strings.Contains(string(body), "during token request") || !strings.Contains(string(body), "status 502") {
			t.Fatalf("body = %q, want token-request failure context", string(body))
		}
		if strings.Contains(string(body), "secret-123") || strings.Contains(string(body), "refreshed-token") {
			t.Fatalf("body leaked auth material: %q", string(body))
		}
		if got := harness.upstreamHitCount(); got != 0 {
			t.Fatalf("protected upstream hits = %d, want 0", got)
		}
		if got := harness.upstreamDialCount(); got != 0 {
			t.Fatalf("protected upstream dials = %d, want 0", got)
		}
		if got := tokenCalls.Load(); got != 1 {
			t.Fatalf("token endpoint calls = %d, want 1", got)
		}
	})

	t.Run("OAuth2 malformed refresh payload and invalid provider config fail closed", func(t *testing.T) {
		t.Parallel()

		t.Run("OAuth2 malformed refresh payload", func(t *testing.T) {
			t.Parallel()

			store := testutil.NewTestSecretStore()
			store.SeedStrings(map[string]string{
				"github.com/example/github-issues/github_oauth/client_id":     "client-123",
				"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
				"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
			})

			var tokenCalls atomic.Int32
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokenCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"expires_in":3600}`))
			}))
			defer tokenServer.Close()

			harness := newProxyHarness(t, proxyHarnessConfig{
				store:        store,
				allowedHosts: []string{"127.0.0.1"},
				rules:        []transport.Rule{oauth2ProxyRule(tokenServer.URL, "127.0.0.1")},
			})

			resp := harness.mustGet(t, "/oauth2-malformed")
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", resp.StatusCode)
			}
			if !strings.Contains(string(body), "response parse") || !strings.Contains(string(body), "access_token is required") {
				t.Fatalf("body = %q, want parse failure context", string(body))
			}
			if got := harness.upstreamHitCount(); got != 0 {
				t.Fatalf("protected upstream hits = %d, want 0", got)
			}
			if got := harness.upstreamDialCount(); got != 0 {
				t.Fatalf("protected upstream dials = %d, want 0", got)
			}
			if got := tokenCalls.Load(); got != 1 {
				t.Fatalf("token endpoint calls = %d, want 1", got)
			}
		})

		t.Run("OAuth2 invalid provider config", func(t *testing.T) {
			t.Parallel()

			store := testutil.NewTestSecretStore()
			store.SeedStrings(map[string]string{
				"github.com/example/github-issues/github_oauth/client_id":     "client-123",
				"github.com/example/github-issues/github_oauth/client_secret": "secret-123",
				"github.com/example/github-issues/github_oauth/refresh_token": "refresh-123",
			})

			harness := newProxyHarness(t, proxyHarnessConfig{
				store:        store,
				allowedHosts: []string{"127.0.0.1"},
				rules: []transport.Rule{{
					Name:               "github_oauth",
					SecretKey:          "github.com/example/github-issues/github_oauth/access_token",
					Type:               tooldef.CredentialTypeOAuth2,
					OAuth2Provider:     &tooldef.OAuth2ProviderConfig{AuthURL: "https://accounts.example.com/oauth/authorize"},
					OAuth2SecretFamily: "github.com/example/github-issues/github_oauth",
					OAuth2CacheKey:     "github.com/example/github-issues:github_oauth",
					Inject: tooldef.CredentialInject{
						Hosts:  []string{"127.0.0.1"},
						Method: "bearer_header",
					},
				}},
			})

			resp := harness.mustGet(t, "/oauth2-invalid-provider")
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", resp.StatusCode)
			}
			if !strings.Contains(string(body), "during provider config") || !strings.Contains(string(body), "token endpoint is required") {
				t.Fatalf("body = %q, want provider config failure context", string(body))
			}
			if got := harness.upstreamHitCount(); got != 0 {
				t.Fatalf("protected upstream hits = %d, want 0", got)
			}
			if got := harness.upstreamDialCount(); got != 0 {
				t.Fatalf("protected upstream dials = %d, want 0", got)
			}
		})
	})

	t.Run("allows matching host without mutation", func(t *testing.T) {
		t.Parallel()

		harness := newProxyHarness(t, proxyHarnessConfig{
			allowedHosts: []string{"127.0.0.1"},
		})

		resp := harness.mustGet(t, "/exact")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		defer resp.Body.Close()

		if got := harness.upstreamHitCount(); got != 1 {
			t.Fatalf("upstream hits = %d, want 1", got)
		}
		obs := harness.singleObservation(t)
		if obs.URL != "https://"+harness.upstreamAddr+"/exact" {
			t.Fatalf("observed URL = %q, want exact upstream URL", obs.URL)
		}
		if auth := obs.RequestHeader.Get("Authorization"); auth != "" {
			t.Fatalf("authorization = %q, want empty", auth)
		}
	})

	t.Run("wildcard and path prefix rules reuse bearer injection", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{"pkg/bearer": "token-123"})
		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"*.example.test"},
			rules: []transport.Rule{{
				Name:      "bearer",
				SecretKey: "pkg/bearer",
				Inject: tooldef.CredentialInject{
					Hosts:      []string{"*.example.test"},
					Method:     "bearer_header",
					PathPrefix: "/v1",
				},
			}},
			upstreamHostOverride: "api.example.test",
		})

		resp := harness.mustGet(t, "/v1/issues")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		defer resp.Body.Close()

		obs := harness.singleObservation(t)
		if got := obs.RequestHeader.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if got := obs.UpstreamHost; got != "api.example.test" {
			t.Fatalf("upstream host = %q, want api.example.test", got)
		}
	})

	t.Run("basic auth mutation reaches only upstream boundary", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{"pkg/basic": "aladdin:open-sesame"})
		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"127.0.0.1"},
			rules: []transport.Rule{{
				Name:      "basic",
				SecretKey: "pkg/basic",
				Inject: tooldef.CredentialInject{
					Hosts:  []string{"127.0.0.1"},
					Method: "basic_auth",
				},
			}},
		})

		resp := harness.mustGet(t, "/basic")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		defer resp.Body.Close()

		obs := harness.singleObservation(t)
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("aladdin:open-sesame"))
		if got := obs.RequestHeader.Get("Authorization"); got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("api key header mutation reaches upstream", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{"pkg/header": "header-secret"})
		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"127.0.0.1"},
			rules: []transport.Rule{{
				Name:      "header-key",
				SecretKey: "pkg/header",
				Inject: tooldef.CredentialInject{
					Hosts:      []string{"127.0.0.1"},
					Method:     "api_key_header",
					HeaderName: "X-API-Key",
				},
			}},
		})

		resp := harness.mustGet(t, "/header")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		defer resp.Body.Close()

		obs := harness.singleObservation(t)
		if got := obs.RequestHeader.Get("X-Api-Key"); got != "header-secret" {
			t.Fatalf("X-API-Key = %q, want injected secret", got)
		}
	})

	t.Run("api key query mutation reaches upstream but denial stays redacted", func(t *testing.T) {
		t.Parallel()

		store := testutil.NewTestSecretStore()
		store.SeedStrings(map[string]string{"pkg/query": "query-secret"})
		harness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"127.0.0.1"},
			rules: []transport.Rule{{
				Name:      "query-key",
				SecretKey: "pkg/query",
				Inject: tooldef.CredentialInject{
					Hosts:     []string{"127.0.0.1"},
					Method:    "api_key_query",
					QueryName: "token",
				},
			}},
		})

		resp := harness.mustGet(t, "/query")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		defer resp.Body.Close()

		obs := harness.singleObservation(t)
		if got := obs.URL; !strings.Contains(got, "token=query-secret") {
			t.Fatalf("observed URL = %q, want injected query param at upstream boundary", got)
		}

		denyHarness := newProxyHarness(t, proxyHarnessConfig{
			store:        store,
			allowedHosts: []string{"example.invalid"},
			rules: []transport.Rule{{
				Name:      "query-key",
				SecretKey: "pkg/query",
				Inject: tooldef.CredentialInject{
					Hosts:     []string{"127.0.0.1"},
					Method:    "api_key_query",
					QueryName: "token",
				},
			}},
		})
		deniedResp := denyHarness.mustGet(t, "/denied-query")
		defer deniedResp.Body.Close()
		body, _ := io.ReadAll(deniedResp.Body)
		if deniedResp.StatusCode != http.StatusForbidden {
			t.Fatalf("deny status = %d, want 403", deniedResp.StatusCode)
		}
		if strings.Contains(string(body), "query-secret") {
			t.Fatalf("denial body leaked injected secret: %q", string(body))
		}
		if got := denyHarness.upstreamHitCount(); got != 0 {
			t.Fatalf("upstream hits on denial = %d, want 0", got)
		}
	})

	t.Run("deny by default returns explicit failure before any upstream dial", func(t *testing.T) {
		t.Parallel()

		harness := newProxyHarness(t, proxyHarnessConfig{})
		resp := harness.mustGet(t, "/deny-default")
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if !strings.Contains(string(body), `transport denied request to host "127.0.0.1": no allowed hosts declared`) {
			t.Fatalf("body = %q, want explicit deny-by-default message", string(body))
		}
		if got := harness.upstreamHitCount(); got != 0 {
			t.Fatalf("upstream hits = %d, want 0", got)
		}
		if got := harness.upstreamDialCount(); got != 0 {
			t.Fatalf("upstream dials = %d, want 0", got)
		}
	})

	t.Run("malformed request reconstruction and upstream dial failures fail closed", func(t *testing.T) {
		t.Parallel()

		if _, err := reconstructHTTPSURL("", &http.Request{URL: mustParseURL(t, "/relative")}); err == nil {
			t.Fatal("expected missing CONNECT target to fail")
		}

		if _, err := rewritePreparedRequest(&http.Request{}, "http://", nil); err == nil {
			t.Fatal("expected malformed prepared URL rewrite to fail")
		}

		proxy, err := New(nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		policy, err := transport.NewPolicy(nil, nil, []string{"127.0.0.1"}, false)
		if err != nil {
			t.Fatalf("NewPolicy: %v", err)
		}
		proxy.Policy = policy

		proxyListener, err := proxy.ListenAndServe()
		if err != nil {
			t.Fatalf("ListenAndServe: %v", err)
		}
		defer proxyListener.Close()

		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(CACertPEM()) {
			t.Fatal("failed to add proxy CA cert")
		}
		proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
		client := &http.Client{Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		}}

		resp, err := client.Get("https://127.0.0.1:1/dial-failure")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
		if !strings.Contains(string(body), "proxy failed to connect upstream") {
			t.Fatalf("body = %q, want upstream dial failure context", string(body))
		}
	})
}

type proxyHarnessConfig struct {
	store                secrets.SecretStore
	allowedHosts         []string
	rules                []transport.Rule
	upstreamHostOverride string
}

type observedExchange struct {
	URL           string
	RequestHeader http.Header
	UpstreamHost  string
}

type proxyHarness struct {
	client               *http.Client
	upstreamAddr         string
	upstreamHostOverride string

	hitCount  *atomic.Int32
	dialCount *atomic.Int32

	obsMu          *sync.Mutex
	observationLog *[]observedExchange
}

func newProxyHarness(t *testing.T, cfg proxyHarnessConfig) *proxyHarness {
	t.Helper()

	certHosts := []string{"127.0.0.1"}
	if cfg.upstreamHostOverride != "" {
		certHosts = append(certHosts, cfg.upstreamHostOverride)
	}
	upstreamCert, upstreamKey := selfSignedCert(t, certHosts...)
	upstream, upstreamAddr, dialCount, hitCount := startCountingUpstream(t, upstreamCert, upstreamKey)
	t.Cleanup(func() { upstream.Close() })

	upstreamPool := x509.NewCertPool()
	upstreamPool.AppendCertsFromPEM(upstreamCert)

	observationsMu := &sync.Mutex{}
	observations := &[]observedExchange{}
	proxy, err := New(ObserverFunc(func(_ string, req *http.Request, _ *http.Response) {
		observationsMu.Lock()
		defer observationsMu.Unlock()
		*observations = append(*observations, observedExchange{
			URL:           req.URL.String(),
			RequestHeader: req.Header.Clone(),
			UpstreamHost:  req.Host,
		})
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	policy, err := transport.NewPolicy(cfg.store, cfg.rules, cfg.allowedHosts, true)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	proxy.Policy = policy
	proxy.UpstreamTLSConfig = &tls.Config{RootCAs: upstreamPool}
	if cfg.upstreamHostOverride != "" {
		proxy.UpstreamDialTLS = func(network, addr string, tlsCfg *tls.Config) (net.Conn, error) {
			if addr == cfg.upstreamHostOverride+":443" {
				addr = upstreamAddr
			}
			return tls.Dial(network, addr, tlsCfg)
		}
	}

	proxyListener, err := proxy.ListenAndServe()
	if err != nil {
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() { proxyListener.Close() })

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(CACertPEM()) {
		t.Fatal("failed to add proxy CA cert")
	}
	proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
	transportCfg := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: caPool},
	}

	return &proxyHarness{
		client:               &http.Client{Transport: transportCfg},
		upstreamAddr:         upstreamAddr,
		upstreamHostOverride: cfg.upstreamHostOverride,
		hitCount:             hitCount,
		dialCount:            dialCount,
		obsMu:                observationsMu,
		observationLog:       observations,
	}
}

func (h *proxyHarness) mustGet(t *testing.T, path string) *http.Response {
	t.Helper()

	targetHost := h.upstreamAddr
	if h.upstreamHostOverride != "" {
		targetHost = h.upstreamHostOverride
	}
	resp, err := h.client.Get("https://" + targetHost + path)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	return resp
}

func (h *proxyHarness) upstreamHitCount() int {
	return int(h.hitCount.Load())
}

func (h *proxyHarness) upstreamDialCount() int {
	return int(h.dialCount.Load())
}

func (h *proxyHarness) singleObservation(t *testing.T) observedExchange {
	t.Helper()
	observations := h.observations(t)
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(observations))
	}
	return observations[0]
}

func (h *proxyHarness) observations(t *testing.T) []observedExchange {
	t.Helper()
	h.obsMu.Lock()
	defer h.obsMu.Unlock()
	out := make([]observedExchange, len(*h.observationLog))
	copy(out, *h.observationLog)
	return out
}

func oauth2ProxyRule(tokenURL string, host string) transport.Rule {
	return transport.Rule{
		Name:               "github_oauth",
		SecretKey:          "github.com/example/github-issues/github_oauth/access_token",
		Type:               tooldef.CredentialTypeOAuth2,
		OAuth2Provider:     &tooldef.OAuth2ProviderConfig{AuthURL: "https://accounts.example.com/oauth/authorize", TokenURL: tokenURL},
		OAuth2SecretFamily: "github.com/example/github-issues/github_oauth",
		OAuth2CacheKey:     "github.com/example/github-issues:github_oauth",
		Inject: tooldef.CredentialInject{
			Hosts:  []string{host},
			Method: "bearer_header",
		},
	}
}

func startCountingUpstream(t *testing.T, certPEM, keyPEM []byte) (*http.Server, string, *atomic.Int32, *atomic.Int32) {
	t.Helper()

	hitCount := &atomic.Int32{}
	dialCount := &atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"path": r.URL.String()})
	})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	counting := &countingListener{Listener: listener, dialCount: dialCount}
	tlsListener := tls.NewListener(counting, &tls.Config{Certificates: []tls.Certificate{tlsCert}})
	server := &http.Server{Handler: mux}
	go server.Serve(tlsListener)
	return server, listener.Addr().String(), dialCount, hitCount
}

type countingListener struct {
	net.Listener
	dialCount *atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.dialCount.Add(1)
	}
	return conn, err
}

func selfSignedCert(t *testing.T, hosts ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	if len(hosts) == 0 {
		t.Fatal("selfSignedCert requires at least one host")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
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

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
