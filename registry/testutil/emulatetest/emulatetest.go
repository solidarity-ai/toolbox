package emulatetest

import (
	"bytes"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testToken           = "ghp_test123"
	startupTimeout      = 30 * time.Second
	startupPollInterval = 200 * time.Millisecond
	httpClientTimeout   = 10 * time.Second
)

var (
	shutdownOnce sync.Once
	sharedMu     sync.Mutex
	sharedStarts = map[string]*sharedStart{}
)

type sharedStart struct {
	once   sync.Once
	server *Server
	skip   string
	err    error
}

// Server manages a shared emulate subprocess for integration tests.
type Server struct {
	service      string
	baseURL      string
	authBaseURL  string
	secureURL    string
	port         int
	cmd          *exec.Cmd
	output       *bytes.Buffer
	waitCh       <-chan error
	client       *http.Client
	rawClient    *http.Client
	secureClient *http.Client
	authProxy    *httptest.Server
	secureProxy  *httptest.Server
}

// Start boots the GitHub emulate service exactly once per test binary and reuses
// the same server for subsequent tests in the package.
func Start(t *testing.T) *Server {
	return StartService(t, "github")
}

// StartGoogle boots the Google OAuth emulate service exactly once per test
// binary and reuses the same server for subsequent tests in the package.
func StartGoogle(t *testing.T) *Server {
	return StartService(t, "google")
}

// StartService boots one emulate service exactly once per test binary and
// reuses the same server for subsequent tests in the package.
func StartService(t *testing.T, service string) *Server {
	t.Helper()
	key := strings.TrimSpace(strings.ToLower(service))
	if key == "" {
		t.Fatal("emulatetest: service is required")
	}

	sharedMu.Lock()
	state, ok := sharedStarts[key]
	if !ok {
		state = &sharedStart{}
		sharedStarts[key] = state
	}
	sharedMu.Unlock()

	state.once.Do(func() {
		state.server, state.skip, state.err = startServer(t, key)
	})

	if state.skip != "" {
		t.Skip(state.skip)
	}
	if state.err != nil {
		t.Fatalf("emulatetest: %v", state.err)
	}

	return state.server
}

// Service returns the emulate service name backing this server.
func (s *Server) Service() string {
	return s.service
}

// BaseURL returns the raw local emulate base URL.
func (s *Server) BaseURL() string {
	return s.baseURL
}

// AuthBaseURL returns the auth-gated façade URL backed by emulate.
func (s *Server) AuthBaseURL() string {
	return s.authBaseURL
}

// SecureURL returns the HTTPS façade URL backed by emulate.
func (s *Server) SecureURL() string {
	return s.secureURL
}

// Client returns an HTTP client with the emulate admin auth header attached.
func (s *Server) Client() *http.Client {
	return s.client
}

// RawClient returns an HTTP client without implicit auth headers.
func (s *Server) RawClient() *http.Client {
	return s.rawClient
}

// SecureClient returns an HTTP client configured to trust the HTTPS façade.
func (s *Server) SecureClient() *http.Client {
	if s == nil || s.secureClient == nil {
		return s.rawClient
	}
	return s.secureClient
}

// SecureProxyCertificatePEM returns the HTTPS façade certificate in PEM form.
func (s *Server) SecureProxyCertificatePEM() []byte {
	if s == nil || s.secureProxy == nil || s.secureProxy.Certificate() == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.secureProxy.Certificate().Raw})
}

// Token returns the shared test token accepted by the auth-gated façade.
func (s *Server) Token() string {
	return testToken
}

// Port returns the TCP port emulate is listening on.
func (s *Server) Port() int {
	return s.port
}

func startServer(t *testing.T, service string) (*Server, string, error) {
	t.Helper()

	npxPath, err := exec.LookPath("npx")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, "emulatetest: npx not available", nil
		}
		return nil, "", fmt.Errorf("locate npx: %w", err)
	}

	port, err := freePort()
	if err != nil {
		return nil, "", fmt.Errorf("reserve free port: %w", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	output := &bytes.Buffer{}
	cmd := exec.Command(npxPath, "emulate", "start", "--service", service, "--port", strconv.Itoa(port))
	cmd.Stdout = output
	cmd.Stderr = output
	applyPlatformSysProcAttr(cmd)

	t.Logf("emulatetest: starting emulate service %q on port %d", service, port)
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, "emulatetest: npx not available", nil
		}
		return nil, "", fmt.Errorf("start emulate: %w\nstderr:\n%s", err, capturedOutput(output))
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	startupClient := &http.Client{Timeout: 2 * time.Second}
	readinessPath, err := readinessProbePath(service)
	if err != nil {
		return nil, "", err
	}
	deadline := time.Now().Add(startupTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		select {
		case err := <-waitCh:
			if err == nil {
				err = fmt.Errorf("process exited before readiness")
			}
			return nil, "", fmt.Errorf("emulate exited before readiness: %w\nstderr:\n%s", err, capturedOutput(output))
		default:
		}

		resp, err := startupClient.Get(baseURL + readinessPath)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				authProxy := newAuthProxy(t, baseURL)
				secureProxy := newTLSProxy(t, baseURL)
				t.Logf("emulatetest: emulate service %q ready at %s", service, baseURL)
				return &Server{
					service:      service,
					baseURL:      baseURL,
					authBaseURL:  authProxy.URL,
					secureURL:    secureProxy.URL,
					port:         port,
					cmd:          cmd,
					output:       output,
					waitCh:       waitCh,
					client:       newAuthedClient(),
					rawClient:    &http.Client{Timeout: httpClientTimeout},
					secureClient: secureProxy.Client(),
					authProxy:    authProxy,
					secureProxy:  secureProxy,
				}, "", nil
			}
			lastErr = fmt.Errorf("GET %s%s returned %d", baseURL, readinessPath, resp.StatusCode)
		} else {
			lastErr = err
		}

		time.Sleep(startupPollInterval)
	}

	killProcess(cmd)
	waitForExit(waitCh)

	if lastErr != nil {
		return nil, "", fmt.Errorf("startup timeout after %s waiting for %s%s: %v\nstderr:\n%s", startupTimeout, baseURL, readinessPath, lastErr, capturedOutput(output))
	}
	return nil, "", fmt.Errorf("startup timeout after %s waiting for %s%s\nstderr:\n%s", startupTimeout, baseURL, readinessPath, capturedOutput(output))
}

func readinessProbePath(service string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(service)) {
	case "github":
		return "/rate_limit", nil
	case "google":
		return "/.well-known/openid-configuration", nil
	default:
		return "", fmt.Errorf("unsupported emulate test service %q", service)
	}
}

func shutdownSharedServer() {
	shutdownOnce.Do(func() {
		sharedMu.Lock()
		defer sharedMu.Unlock()
		for _, state := range sharedStarts {
			if state == nil || state.server == nil {
				continue
			}
			if state.server.authProxy != nil {
				state.server.authProxy.Close()
			}
			if state.server.secureProxy != nil {
				state.server.secureProxy.Close()
			}
			if state.server.cmd == nil {
				continue
			}
			killProcess(state.server.cmd)
			waitForExit(state.server.waitCh)
		}
	})
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener addr type %T", listener.Addr())
	}
	return addr.Port, nil
}

func newAuthedClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout: httpClientTimeout,
		Transport: authTransport{
			base:  transport,
			token: testToken,
		},
	}
}

func newAuthProxy(t *testing.T, upstreamBaseURL string) *httptest.Server {
	t.Helper()
	plainClient := &http.Client{Timeout: httpClientTimeout}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authGatedPath(r) {
			if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer "+testToken {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"message":"auth-gated emulate route requires Authorization: Bearer <redacted>"}`)
				return
			}
		}

		upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimRight(upstreamBaseURL, "/")+r.URL.RequestURI(), r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("build upstream request: %v", err), http.StatusBadGateway)
			return
		}
		upstreamReq.Header = r.Header.Clone()

		resp, err := plainClient.Do(upstreamReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("proxy emulate request: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
}

func newTLSProxy(t *testing.T, upstreamBaseURL string) *httptest.Server {
	t.Helper()
	plainClient := &http.Client{Timeout: httpClientTimeout}

	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimRight(upstreamBaseURL, "/")+r.URL.RequestURI(), r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("build upstream request: %v", err), http.StatusBadGateway)
			return
		}
		upstreamReq.Header = r.Header.Clone()

		resp, err := plainClient.Do(upstreamReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("proxy emulate request: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
}

func authGatedPath(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 5 && parts[0] == "repos" && parts[3] == "issues"
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header.Get("Authorization") == "" {
		clone.Header.Set("Authorization", "token "+t.token)
	}
	return t.base.RoundTrip(clone)
}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func waitForExit(waitCh <-chan error) {
	if waitCh == nil {
		return
	}
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
	}
}

func capturedOutput(buf *bytes.Buffer) string {
	if buf == nil {
		return "<no emulate output captured>"
	}
	text := strings.TrimSpace(buf.String())
	if text == "" {
		return "<no emulate output captured>"
	}
	return text
}
