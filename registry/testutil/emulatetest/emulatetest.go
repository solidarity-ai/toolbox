package emulatetest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	startOnce    sync.Once
	shutdownOnce sync.Once

	sharedServer *Server
	sharedSkip   string
	sharedErr    error
)

// Server manages a shared emulate subprocess for integration tests.
type Server struct {
	baseURL string
	port    int
	cmd     *exec.Cmd
	output  *bytes.Buffer
	waitCh  <-chan error
	client  *http.Client
}

// Start boots emulate exactly once per test binary and reuses the same server
// for subsequent tests in the package.
func Start(t *testing.T) *Server {
	t.Helper()

	startOnce.Do(func() {
		sharedServer, sharedSkip, sharedErr = startServer(t)
	})

	if sharedSkip != "" {
		t.Skip(sharedSkip)
	}
	if sharedErr != nil {
		t.Fatalf("emulatetest: %v", sharedErr)
	}

	return sharedServer
}

// BaseURL returns the local emulate base URL.
func (s *Server) BaseURL() string {
	return s.baseURL
}

// Client returns an HTTP client with the emulate admin auth header attached.
func (s *Server) Client() *http.Client {
	return s.client
}

// Port returns the TCP port emulate is listening on.
func (s *Server) Port() int {
	return s.port
}

func startServer(t *testing.T) (*Server, string, error) {
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
	cmd := exec.Command(npxPath, "emulate", "start", "--service", "github", "--port", strconv.Itoa(port))
	cmd.Stdout = output
	cmd.Stderr = output
	applyPlatformSysProcAttr(cmd)

	t.Logf("emulatetest: starting emulate on port %d", port)
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

		resp, err := startupClient.Get(baseURL + "/rate_limit")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("emulatetest: emulate ready at %s", baseURL)
				return &Server{
					baseURL: baseURL,
					port:    port,
					cmd:     cmd,
					output:  output,
					waitCh:  waitCh,
					client:  newAuthedClient(),
				}, "", nil
			}
			lastErr = fmt.Errorf("GET %s/rate_limit returned %d", baseURL, resp.StatusCode)
		} else {
			lastErr = err
		}

		time.Sleep(startupPollInterval)
	}

	killProcess(cmd)
	waitForExit(waitCh)

	if lastErr != nil {
		return nil, "", fmt.Errorf("startup timeout after %s waiting for %s/rate_limit: %v\nstderr:\n%s", startupTimeout, baseURL, lastErr, capturedOutput(output))
	}
	return nil, "", fmt.Errorf("startup timeout after %s waiting for %s/rate_limit\nstderr:\n%s", startupTimeout, baseURL, capturedOutput(output))
}

func shutdownSharedServer() {
	shutdownOnce.Do(func() {
		if sharedServer == nil || sharedServer.cmd == nil {
			return
		}
		killProcess(sharedServer.cmd)
		waitForExit(sharedServer.waitCh)
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
