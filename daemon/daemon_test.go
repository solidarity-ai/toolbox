//go:build !windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	daemonprocessctl "github.com/solidarity-ai/toolbox/daemon/internal/processctl"
	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	"github.com/solidarity-ai/toolbox/secrets"
)

var (
	testToolboxBinary string
	testPeerHelper    string
)

func TestMain(m *testing.M) {
	code := 1
	_ = os.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_WORK_FACTOR", "10")
	_ = os.Setenv("TOOLBOX_SECRET_STORE_SCRYPT_MAX_WORK_FACTOR", "10")
	if err := buildTestBinaries(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		code = m.Run()
	}
	os.Exit(code)
}

func TestClientPing(t *testing.T) {
	_, _ = startTrackedServer(t)

	client, err := EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	resp, err := client.Ping()
	if err != nil {
		t.Fatalf("Ping(): %v", err)
	}
	if resp.Payload != "pong" {
		t.Fatalf("payload = %q, want pong", resp.Payload)
	}
	if resp.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", resp.PID, os.Getpid())
	}
}

func TestOAuthServiceReachableThroughVerifiedDaemonClient(t *testing.T) {
	_, _ = startTrackedServer(t)

	client, err := EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	urls, err := client.OAuthRedirectURI(context.Background())
	if err == nil {
		t.Fatalf("OAuthRedirectURI() = %#v, want unavailable before daemon webserver address is registered", urls)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("OAuthRedirectURI() error = %T %[1]v, want connect error", err)
	}
	if connectErr.Code() != connect.CodeUnavailable {
		t.Fatalf("OAuthRedirectURI() code = %v, want %v", connectErr.Code(), connect.CodeUnavailable)
	}
	if got := connectErr.Message(); !strings.Contains(got, "daemon OAuth webserver is unavailable") {
		t.Fatalf("OAuthRedirectURI() message = %q, want webserver unavailable message", got)
	}
}

func TestOAuthRedirectURIUsesRegisteredDaemonWebserverAddress(t *testing.T) {
	_, srv := startTrackedServer(t)
	srv.SetOAuthWebserverAddress("127.0.0.1:45678")

	client, err := EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	urls, err := client.OAuthRedirectURI(context.Background())
	if err != nil {
		t.Fatalf("OAuthRedirectURI(): %v", err)
	}
	if urls.RedirectURI != "http://localhost:45678/oauth2/callback" {
		t.Fatalf("RedirectURI = %q, want localhost actual port callback", urls.RedirectURI)
	}
	if urls.DaemonURL != "http://localhost:45678/" {
		t.Fatalf("DaemonURL = %q, want localhost actual port UI", urls.DaemonURL)
	}
}

func TestOAuthRedirectURIPreservesToolboxHostBasePath(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		redirectURI string
		daemonURL   string
	}{
		{
			name:        "base path",
			host:        " https://example.test/base/ ",
			redirectURI: "https://example.test/base/oauth2/callback",
			daemonURL:   "https://example.test/base/",
		},
		{
			name:        "escaped base path and empty query",
			host:        "https://example.test/a%20b?",
			redirectURI: "https://example.test/a%20b/oauth2/callback",
			daemonURL:   "https://example.test/a%20b/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(OAuthHostEnv, tt.host)
			_, srv := startTrackedServer(t)
			srv.SetOAuthWebserverAddress("127.0.0.1:45678")

			client, err := EnsureConnection()
			if err != nil {
				t.Fatalf("EnsureConnection(): %v", err)
			}
			t.Cleanup(func() {
				_ = client.Close()
			})

			urls, err := client.OAuthRedirectURI(context.Background())
			if err != nil {
				t.Fatalf("OAuthRedirectURI(): %v", err)
			}
			if urls.RedirectURI != tt.redirectURI {
				t.Fatalf("RedirectURI = %q, want %q", urls.RedirectURI, tt.redirectURI)
			}
			if urls.DaemonURL != tt.daemonURL {
				t.Fatalf("DaemonURL = %q, want %q", urls.DaemonURL, tt.daemonURL)
			}
		})
	}
}

func TestOAuthRedirectURIRejectsInvalidToolboxHost(t *testing.T) {
	t.Setenv(OAuthHostEnv, "not-a-url")
	_, srv := startTrackedServer(t)
	srv.SetOAuthWebserverAddress("127.0.0.1:45678")

	client, err := EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	_, err = client.OAuthRedirectURI(context.Background())
	if err == nil {
		t.Fatal("OAuthRedirectURI() succeeded with invalid TOOLBOX_HOST, want error")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("OAuthRedirectURI() error = %T %[1]v, want connect error", err)
	}
	if connectErr.Code() != connect.CodeUnavailable {
		t.Fatalf("OAuthRedirectURI() code = %v, want %v", connectErr.Code(), connect.CodeUnavailable)
	}
	if got := connectErr.Message(); !strings.Contains(got, "TOOLBOX_HOST") {
		t.Fatalf("OAuthRedirectURI() message = %q, want TOOLBOX_HOST validation message", got)
	}
}

func TestPendingOAuthFlowsReturnsOnlyPendingSnapshots(t *testing.T) {
	_, srv := startTrackedServer(t)

	client, err := EnsureConnection()
	if err != nil {
		t.Fatalf("EnsureConnection(): %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	ctx := context.Background()
	first, err := client.OAuthBegin(ctx, OAuthBeginOptions{
		State:            "state-pending-first",
		AuthorizationURL: "https://provider.example.test/authorize?flow=first",
		Label:            "Authorize first credential",
	})
	if err != nil {
		t.Fatalf("OAuthBegin(first): %v", err)
	}
	second, err := client.OAuthBegin(ctx, OAuthBeginOptions{
		State:            "state-pending-second",
		AuthorizationURL: "https://provider.example.test/authorize?flow=second",
		Label:            "Authorize second credential",
	})
	if err != nil {
		t.Fatalf("OAuthBegin(second): %v", err)
	}

	pending := srv.PendingOAuthFlows()
	if len(pending) != 2 {
		t.Fatalf("PendingOAuthFlows() len = %d, want 2: %#v", len(pending), pending)
	}
	if pending[0].FlowID != first.FlowID || pending[0].State != "state-pending-first" ||
		pending[0].AuthorizationURL != "https://provider.example.test/authorize?flow=first" ||
		pending[0].Label != "Authorize first credential" ||
		pending[0].CreatedAt.IsZero() || pending[0].ExpiresAt.IsZero() {
		t.Fatalf("first pending snapshot = %#v, want flow/state/url/label/timestamps", pending[0])
	}
	if pending[1].FlowID != second.FlowID || pending[1].State != "state-pending-second" {
		t.Fatalf("second pending snapshot = %#v, want second flow", pending[1])
	}

	if err := client.OAuthCancel(ctx, first.FlowID); err != nil {
		t.Fatalf("OAuthCancel(first): %v", err)
	}
	pending = srv.PendingOAuthFlows()
	if len(pending) != 1 || pending[0].FlowID != second.FlowID {
		t.Fatalf("PendingOAuthFlows() after cancel = %#v, want only second flow", pending)
	}

	if err := srv.CompleteOAuthFlow("state-pending-second", OAuthCallbackResult{Code: "abc"}); err != nil {
		t.Fatalf("CompleteOAuthFlow(second): %v", err)
	}
	if pending = srv.PendingOAuthFlows(); len(pending) != 0 {
		t.Fatalf("PendingOAuthFlows() after completion = %#v, want none", pending)
	}
}

func TestPeerVerificationRejectsDifferentBinary(t *testing.T) {
	socketPath, _ := startTrackedServer(t)

	cmd := exec.Command(testPeerHelper, socketPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("peer helper unexpectedly succeeded: %s", strings.TrimSpace(string(output)))
	}
}

func TestSessionRegistrationTracksClients(t *testing.T) {
	_, srv := startTrackedServer(t)
	registry := srv.Registry()

	reg, err := OpenSessionRegistration(SessionState{
		Mode:          "codemode_repl",
		WorkingDir:    "/tmp/work",
		PreparedTools: []string{"example.com/pkg@v1.2.3/calc.add"},
	})
	if err != nil {
		t.Fatalf("OpenSessionRegistration(): %v", err)
	}

	waitForClientCount(t, registry, 1)
	clients := registry.Clients()
	if len(clients) != 1 {
		t.Fatalf("Clients() len = %d, want 1", len(clients))
	}
	if clients[0].Mode != "codemode_repl" || clients[0].WorkingDir != "/tmp/work" {
		t.Fatalf("client = %#v, want codemode_repl /tmp/work", clients[0])
	}
	if len(clients[0].PreparedTools) != 1 || clients[0].PreparedTools[0] != "example.com/pkg@v1.2.3/calc.add" {
		t.Fatalf("PreparedTools = %#v", clients[0].PreparedTools)
	}
	if clients[0].PID <= 0 {
		t.Fatalf("PID = %d, want > 0", clients[0].PID)
	}
	if clients[0].LastSyncAt.IsZero() {
		t.Fatal("LastSyncAt is zero, want sync timestamp")
	}

	if err := reg.Update(SessionState{
		Mode:          "codemode_repl",
		WorkingDir:    "/tmp/work",
		PreparedTools: []string{"example.com/pkg@v1.2.3/calc.sub"},
	}); err != nil {
		t.Fatalf("Update(): %v", err)
	}

	waitForPreparedTool(t, registry, "example.com/pkg@v1.2.3/calc.sub")
	if err := reg.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	waitForClientCount(t, registry, 0)
}

func TestSessionRegistrationTracksPendingApprovals(t *testing.T) {
	_, srv := startTrackedServer(t)
	registry := srv.Registry()

	reg, err := OpenSessionRegistration(SessionState{
		Mode:       "codemode_repl",
		WorkingDir: "/tmp/work",
		PendingApprovals: []PendingApprovalSnapshot{
			{ToolCallID: "tc-1", ToolName: "issues.get", ParamsInspect: `{id: "I-1", meta: {team: {owner: {name: "alpha"}}}}`},
			{ToolCallID: "tc-2", ToolName: "issues.get", ParamsInspect: `{id: "I-2", meta: {team: {owner: {name: "beta"}}}}`},
		},
	})
	if err != nil {
		t.Fatalf("OpenSessionRegistration(): %v", err)
	}
	defer func() {
		if err := reg.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	waitForClientCount(t, registry, 1)
	clients := registry.Clients()
	if len(clients) != 1 {
		t.Fatalf("Clients() len = %d, want 1", len(clients))
	}
	if len(clients[0].PendingApprovals) != 2 {
		t.Fatalf("PendingApprovals len = %d, want 2", len(clients[0].PendingApprovals))
	}
	if !strings.Contains(clients[0].PendingApprovals[0].ParamsInspect, `owner: {name: "alpha"}`) {
		t.Fatalf("PendingApprovals[0].ParamsInspect = %q, want deep params", clients[0].PendingApprovals[0].ParamsInspect)
	}
}

func TestSessionRegistrationSecretEpochHandlerFiresOnSecretChanges(t *testing.T) {
	_, srv := startTrackedServer(t)

	reg, err := OpenSessionRegistration(SessionState{
		Mode:       "codemode_mcp",
		WorkingDir: "/tmp/work",
	})
	if err != nil {
		t.Fatalf("OpenSessionRegistration(): %v", err)
	}
	defer func() {
		if err := reg.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	var changes atomic.Int32
	reg.SetSecretEpochHandler(func() {
		changes.Add(1)
	})

	ctx := context.Background()
	if _, err := srv.SetupSecretStore(ctx, "test-secret-key"); err != nil {
		t.Fatalf("SetupSecretStore(): %v", err)
	}
	store := NewSecretStore("test-secret-key")
	if err := store.Unlock(ctx, "test-secret-key"); err != nil {
		t.Fatalf("Unlock(): %v", err)
	}
	waitForAtomicCount(t, &changes, 1)

	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	waitForAtomicCount(t, &changes, 2)

	if err := store.Delete(ctx, "service/token"); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	waitForAtomicCount(t, &changes, 3)

	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	waitForAtomicCount(t, &changes, 4)
}

func TestEnsureConnection(t *testing.T) {
	dir := newDaemonTempDir(t)
	t.Cleanup(func() {
		killDaemonFromDir(t, dir)
	})

	pong, pid1 := runToolboxPing(t, dir)
	if pong != "pong" {
		t.Fatalf("first ping = %q, want pong", pong)
	}
	if pid1 <= 0 {
		t.Fatalf("first pid = %d, want > 0", pid1)
	}

	killProcess(t, pid1)
	waitForDeadProcess(t, pid1)

	pong, pid2 := runToolboxPing(t, dir)
	if pong != "pong" {
		t.Fatalf("second ping = %q, want pong", pong)
	}
	if pid2 <= 0 {
		t.Fatalf("second pid = %d, want > 0", pid2)
	}
	if pid1 == pid2 {
		t.Fatalf("relaunch pid = %d, want different from %d", pid2, pid1)
	}
}

func TestSecretStoreRoundTrip(t *testing.T) {
	_, srv := startTrackedServer(t)

	ctx := context.Background()
	lockedStore := NewSecretStore("")
	if _, err := lockedStore.List(ctx, ""); !errors.Is(err, secrets.ErrLocked) {
		t.Fatalf("List() error = %v, want ErrLocked", err)
	}

	store := NewSecretStore("test-secret-key")
	if _, err := srv.SetupSecretStore(ctx, "test-secret-key"); err != nil {
		t.Fatalf("SetupSecretStore(): %v", err)
	}
	if err := store.Set(ctx, "service/token", []byte("value")); err != nil {
		t.Fatalf("Set(): %v", err)
	}

	value, err := store.Get(ctx, "service/token")
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if string(value) != "value" {
		t.Fatalf("Get() = %q, want value", string(value))
	}

	keys, err := store.List(ctx, "service/")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(keys) != 1 || keys[0] != "service/token" {
		t.Fatalf("List() = %#v, want [service/token]", keys)
	}

	if err := store.Delete(ctx, "service/token"); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if _, err := store.Get(ctx, "service/token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}

	if err := store.Lock(ctx); err != nil {
		t.Fatalf("Lock(): %v", err)
	}
	if _, err := lockedStore.List(ctx, ""); !errors.Is(err, secrets.ErrLocked) {
		t.Fatalf("List() after lock error = %v, want ErrLocked", err)
	}
}

func TestRacyLaunch(t *testing.T) {
	dir := newDaemonTempDir(t)
	t.Cleanup(func() {
		killDaemonFromDir(t, dir)
	})

	const callers = 20
	type result struct {
		pid int
		err error
	}
	results := make(chan result, callers)

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, pid, err := runToolboxPingOutput(dir)
			if err != nil {
				results <- result{err: err}
				return
			}
			if pid <= 0 {
				results <- result{err: fmt.Errorf("pid = %d, want > 0", pid)}
				return
			}
			results <- result{pid: pid}
		}()
	}
	wg.Wait()
	close(results)

	var wantPID int
	for res := range results {
		if res.err != nil {
			t.Fatal(res.err)
		}
		if wantPID == 0 {
			wantPID = res.pid
			continue
		}
		if res.pid != wantPID {
			t.Fatalf("observed daemon pids = %d and %d, want one daemon", wantPID, res.pid)
		}
	}
	if wantPID == 0 {
		t.Fatal("wantPID = 0, want launched daemon pid")
	}
	if !daemonprocessctl.IsProcessAlive(wantPID) {
		t.Fatalf("daemon pid %d is not alive", wantPID)
	}
}

func TestStaleCleanup(t *testing.T) {
	dir := newDaemonTempDir(t)
	t.Cleanup(func() {
		killDaemonFromDir(t, dir)
	})

	t.Setenv(daemonpaths.DaemonDirEnv, dir)
	socketPath, err := daemonpaths.SocketPath()
	if err != nil {
		t.Fatalf("SocketPath(): %v", err)
	}
	pidPath, err := daemonpaths.PIDPath()
	if err != nil {
		t.Fatalf("PIDPath(): %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", socketPath, err)
	}
	if err := os.WriteFile(pidPath, []byte("-1"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", pidPath, err)
	}

	pong, pid := runToolboxPing(t, dir)
	if pong != "pong" {
		t.Fatalf("ping = %q, want pong", pong)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d, want > 0", pid)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatalf("Lstat(%q): %v", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s mode = %v, want socket", socketPath, info.Mode())
	}
}

func startTrackedServer(t *testing.T) (string, *Server) {
	t.Helper()

	dir := newDaemonTempDir(t)
	t.Setenv(daemonpaths.DaemonDirEnv, dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	socketPath, err := daemonpaths.SocketPath()
	if err != nil {
		t.Fatalf("SocketPath(): %v", err)
	}
	pidPath, err := daemonpaths.PIDPath()
	if err != nil {
		t.Fatalf("PIDPath(): %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	srv := NewServer(listener)
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Serve(): %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for test server shutdown")
		}
		_ = os.Remove(socketPath)
		_ = os.Remove(pidPath)
	})

	return socketPath, srv
}

func newDaemonTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "toolbox-daemon-")
	if err != nil {
		t.Fatalf("MkdirTemp(): %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func buildTestBinaries() error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Dir(wd)

	binDir, err := os.MkdirTemp("", "toolbox-daemon-test-*")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}

	testToolboxBinary = filepath.Join(binDir, "toolbox")
	testPeerHelper = filepath.Join(binDir, "peer-helper")

	builds := []struct {
		out string
		pkg string
	}{
		{out: testToolboxBinary, pkg: "./cmd/toolbox"},
		{out: testPeerHelper, pkg: "./daemon/testhelper"},
	}
	for _, build := range builds {
		cmd := exec.Command("go", "build", "-o", build.out, build.pkg)
		cmd.Dir = repoRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("go build %s: %w\n%s", build.pkg, err, output)
		}
	}
	return nil
}

func runToolboxPing(t *testing.T, dir string) (string, int) {
	t.Helper()

	output, pid, err := runToolboxPingOutput(dir)
	if err != nil {
		t.Fatalf("runToolboxPingOutput(): %v", err)
	}
	return output, pid
}

func runToolboxPingOutput(dir string) (string, int, error) {
	cmd := exec.Command(testToolboxBinary, "_daemon", "ping", "--pid")
	cmd.Env = append(os.Environ(), daemonpaths.DaemonDirEnv+"="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", 0, fmt.Errorf("toolbox ping failed: %w\n%s", err, output)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) != 2 {
		return "", 0, fmt.Errorf("unexpected ping output %q", strings.TrimSpace(string(output)))
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, fmt.Errorf("parse pid from %q: %w", fields[1], err)
	}
	return fields[0], pid, nil
}

func killDaemonFromDir(t *testing.T, dir string) {
	t.Helper()

	pidPath := filepath.Join(dir, "daemon.pid")
	pid, err := daemonprocessctl.ReadPIDFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatalf("ReadPIDFile(%q): %v", pidPath, err)
	}
	killProcess(t, pid)
	waitForDeadProcess(t, pid)
}

func killProcess(t *testing.T, pid int) {
	t.Helper()

	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill(%d): %v", pid, err)
	}
}

func waitForDeadProcess(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !daemonprocessctl.IsProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pid %d is still alive after timeout", pid)
}

type clientSnapshotSource interface {
	Clients() []ClientSnapshot
}

func waitForClientCount(t *testing.T, source clientSnapshotSource, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(source.Clients()); got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Clients() len did not reach %d; got %#v", want, source.Clients())
}

func waitForPreparedTool(t *testing.T, source clientSnapshotSource, want string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clients := source.Clients()
		if len(clients) == 1 && len(clients[0].PreparedTools) == 1 && clients[0].PreparedTools[0] == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("PreparedTools did not reach %q; got %#v", want, source.Clients())
}

func waitForAtomicCount(t *testing.T, count *atomic.Int32, want int32) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("atomic count did not reach %d; got %d", want, count.Load())
}
