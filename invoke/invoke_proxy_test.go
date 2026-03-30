package invoke

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/runtime/quickts"
	"github.com/solidarity-ai/toolbox/runtime/tswasmcli"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/transport"
	"github.com/solidarity-ai/toolbox/transport/mitmproxy"
)

func TestRunTSWasmExecutableProxyPlumbsEnvMountsAndCleansUp(t *testing.T) {
	tool := proxyTestTool(t)
	policy := mustRuntimePolicy(t, []string{"httpbin.org"})

	originalRunner := runTSWasmCLI
	defer func() { runTSWasmCLI = originalRunner }()

	var captured tswasmcli.Request
	runTSWasmCLI = func(req tswasmcli.Request) (tswasmcli.Result, error) {
		captured = req

		if got := req.Env["SSL_CERT_FILE"]; got != tswasmProxyGuestCACertPath {
			t.Fatalf("SSL_CERT_FILE = %q, want %q", got, tswasmProxyGuestCACertPath)
		}
		if got := req.Env["HTTPS_PROXY"]; !strings.HasPrefix(got, "http://"+tswasmProxyHost+":") {
			t.Fatalf("HTTPS_PROXY = %q, want proxy host prefix", got)
		}
		if len(req.Mounts) != 1 {
			t.Fatalf("mount count = %d, want 1", len(req.Mounts))
		}
		mount := req.Mounts[0]
		if mount.GuestPath != tswasmProxyGuestEtcPath {
			t.Fatalf("guest mount path = %q, want %q", mount.GuestPath, tswasmProxyGuestEtcPath)
		}
		hostsBytes, err := os.ReadFile(filepath.Join(mount.HostPath, "hosts"))
		if err != nil {
			t.Fatalf("read mounted hosts file: %v", err)
		}
		if !strings.Contains(string(hostsBytes), tswasmProxyHost) {
			t.Fatalf("hosts file missing proxy hostname: %q", string(hostsBytes))
		}
		caBytes, err := os.ReadFile(filepath.Join(mount.HostPath, "ssl", "certs", "ca-certificates.crt"))
		if err != nil {
			t.Fatalf("read mounted ca bundle: %v", err)
		}
		if string(caBytes) != string(mitmproxy.CACertPEM()) {
			t.Fatalf("mounted CA bundle mismatch")
		}
		return tswasmcli.Result{Stdout: "ok"}, nil
	}

	result, err := runTSWasmExecutable(tool, "http-client", nil, "/tmp/toolbox-vfs.sock", policy)
	if err != nil {
		t.Fatalf("runTSWasmExecutable: %v", err)
	}
	if result.Stdout != "ok" {
		t.Fatalf("stdout = %q, want ok", result.Stdout)
	}

	mountDir := captured.Mounts[0].HostPath
	if _, err := os.Stat(mountDir); !os.IsNotExist(err) {
		t.Fatalf("proxy support dir still exists after cleanup: %s (err=%v)", mountDir, err)
	}

	proxyURL, err := url.Parse(captured.Env["HTTPS_PROXY"])
	if err != nil {
		t.Fatalf("parse HTTPS_PROXY: %v", err)
	}
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", proxyURL.Port()), 150*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("proxy listener %s still accepting connections after cleanup", proxyURL.Port())
	}
}

func TestRunTSWasmExecutableProxyCleanupOnRuntimeFailure(t *testing.T) {
	tool := proxyTestTool(t)
	policy := mustRuntimePolicy(t, []string{"httpbin.org"})

	originalRunner := runTSWasmCLI
	defer func() { runTSWasmCLI = originalRunner }()

	var captured tswasmcli.Request
	runTSWasmCLI = func(req tswasmcli.Request) (tswasmcli.Result, error) {
		captured = req
		return tswasmcli.Result{}, errors.New("sandbox launch failed")
	}

	_, err := runTSWasmExecutable(tool, "http-client", nil, "/tmp/toolbox-vfs.sock", policy)
	if err == nil {
		t.Fatal("expected sandbox launch failure")
	}
	if !strings.Contains(err.Error(), "sandbox launch failed") {
		t.Fatalf("error = %v, want sandbox launch failed", err)
	}
	if _, err := os.Stat(captured.Mounts[0].HostPath); !os.IsNotExist(err) {
		t.Fatalf("proxy support dir still exists after failed run: %s (err=%v)", captured.Mounts[0].HostPath, err)
	}
}

func TestRunTSWasmExecutableWithoutTransportPolicyLeavesProxyUnset(t *testing.T) {
	tool := proxyTestTool(t)

	originalRunner := runTSWasmCLI
	defer func() { runTSWasmCLI = originalRunner }()

	var captured tswasmcli.Request
	runTSWasmCLI = func(req tswasmcli.Request) (tswasmcli.Result, error) {
		captured = req
		return tswasmcli.Result{Stdout: "ok"}, nil
	}

	_, err := runTSWasmExecutable(tool, "http-client", []string{"--flag"}, "/tmp/toolbox-vfs.sock", nil)
	if err != nil {
		t.Fatalf("runTSWasmExecutable: %v", err)
	}
	if len(captured.Env) != 0 {
		t.Fatalf("env = %#v, want empty", captured.Env)
	}
	if len(captured.Mounts) != 0 {
		t.Fatalf("mounts = %#v, want empty", captured.Mounts)
	}
}

func TestRunTSWasmExecutableProxyStartupFailureFailsBeforeSandbox(t *testing.T) {
	tool := proxyTestTool(t)
	policy := mustRuntimePolicy(t, []string{"httpbin.org"})

	originalRunner := runTSWasmCLI
	originalProxyFactory := newMITMProxy
	defer func() {
		runTSWasmCLI = originalRunner
		newMITMProxy = originalProxyFactory
	}()

	runnerCalled := false
	runTSWasmCLI = func(req tswasmcli.Request) (tswasmcli.Result, error) {
		runnerCalled = true
		return tswasmcli.Result{}, nil
	}
	newMITMProxy = func() (*mitmproxy.Proxy, error) {
		return nil, errors.New("proxy constructor boom")
	}

	_, err := runTSWasmExecutable(tool, "http-client", nil, "/tmp/toolbox-vfs.sock", policy)
	if err == nil {
		t.Fatal("expected proxy startup failure")
	}
	if !strings.Contains(err.Error(), "proxy constructor boom") {
		t.Fatalf("error = %v, want proxy constructor boom", err)
	}
	if runnerCalled {
		t.Fatal("sandbox runner was called after proxy startup failed")
	}
}

func proxyTestTool(t *testing.T) tooldef.ResolvedTool {
	t.Helper()

	packageRoot := t.TempDir()
	relWasmPath := filepath.Join("dist", "http-client.wasm")
	if err := os.MkdirAll(filepath.Join(packageRoot, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, relWasmPath), []byte("dummy wasm"), 0o644); err != nil {
		t.Fatalf("write dummy wasm: %v", err)
	}

	pkg := &tooldef.Package{Runtime: tooldef.RuntimeTypeScriptWasip2Sandbox}
	return tooldef.ResolvedTool{
		Name:    "http-client.fetch",
		Package: pkg,
		TSWasm: &tooldef.TSWasmToolDef{
			TSToolDef: tooldef.TSToolDef{PackageRoot: packageRoot},
			Executables: map[string]string{
				"http-client": relWasmPath,
			},
		},
	}
}

func mustRuntimePolicy(t *testing.T, allowedHosts []string) *transport.Policy {
	t.Helper()
	policy, err := transport.NewPolicy(nil, nil, allowedHosts, true)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

var _ quickts.ExecResult
