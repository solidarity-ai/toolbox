package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
)

func TestDaemonBindAddress(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "")

	if got, explicit := daemonBindAddress(); got != defaultDaemonBindAddress || explicit {
		t.Fatalf("daemonBindAddress() = (%q, %t), want (%q, false)", got, explicit, defaultDaemonBindAddress)
	}

	t.Setenv(daemonBindAddressEnv, "127.0.0.1:9555")
	if got, explicit := daemonBindAddress(); got != "127.0.0.1:9555" || !explicit {
		t.Fatalf("daemonBindAddress() with env = (%q, %t), want (%q, true)", got, explicit, "127.0.0.1:9555")
	}
}

func TestStartDaemonDebugServerServesPingAndEcho(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	var stderr bytes.Buffer
	closeServer, addr, err := startDaemonDebugServer(&stderr, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	if closeServer == nil {
		t.Fatal("closeServer = nil, want close function")
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()
	if addr == "" {
		t.Fatal("addr = empty, want listener address")
	}

	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/ping): %v", err)
	}
	if string(body) != "pong" {
		t.Fatalf("/ping body = %q, want pong", string(body))
	}

	resp, err = http.Get("http://" + addr + "/echo?payload=hello")
	if err != nil {
		t.Fatalf("GET /echo: %v", err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/echo): %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("/echo body = %q, want hello", string(body))
	}
}

func TestStartDaemonDebugServerServesClients(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")

	want := []daemon.ClientSnapshot{{
		PID:           123,
		Mode:          "codemode_repl",
		WorkingDir:    "/tmp/work",
		PreparedTools: []string{"example.com/pkg@v1.2.3/calc.add"},
	}}

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, func() []daemon.ClientSnapshot {
		return want
	})
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	resp, err := http.Get("http://" + addr + "/clients")
	if err != nil {
		t.Fatalf("GET /clients: %v", err)
	}
	defer resp.Body.Close()

	var got []daemon.ClientSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Decode(/clients): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("/clients len = %d, want 1", len(got))
	}
	if got[0].PID != want[0].PID || got[0].Mode != want[0].Mode || got[0].WorkingDir != want[0].WorkingDir {
		t.Fatalf("/clients[0] = %#v, want %#v", got[0], want[0])
	}
	if len(got[0].PreparedTools) != 1 || got[0].PreparedTools[0] != want[0].PreparedTools[0] {
		t.Fatalf("/clients[0].PreparedTools = %#v, want %#v", got[0].PreparedTools, want[0].PreparedTools)
	}
}

func TestStartDaemonDebugServerExitTriggersShutdown(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "1")

	var shutdownCalls atomic.Int32
	shutdownCh := make(chan struct{}, 1)
	closeServer, addr, err := startDaemonDebugServer(io.Discard, func() {
		shutdownCalls.Add(1)
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	}, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/exit", nil)
	if err != nil {
		t.Fatalf("NewRequest(/exit): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /exit: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/exit): %v", err)
	}
	if string(body) != "shutting down" {
		t.Fatalf("/exit body = %q, want shutting down", string(body))
	}

	select {
	case <-shutdownCh:
	case <-time.After(2 * time.Second):
		t.Fatal("/exit did not trigger shutdown")
	}
	if shutdownCalls.Load() != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalls.Load())
	}
}

func TestStartDaemonDebugServerKillTriggersHardExit(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "1")

	var killCalls atomic.Int32
	killCh := make(chan struct{}, 1)
	prev := daemonHardExit
	daemonHardExit = func() {
		killCalls.Add(1)
		select {
		case killCh <- struct{}{}:
		default:
		}
	}
	defer func() {
		daemonHardExit = prev
	}()

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/kill", nil)
	if err != nil {
		t.Fatalf("NewRequest(/kill): %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /kill: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(/kill): %v", err)
	}
	if string(body) != "killing process" {
		t.Fatalf("/kill body = %q, want killing process", string(body))
	}

	select {
	case <-killCh:
	case <-time.After(2 * time.Second):
		t.Fatal("/kill did not trigger hard exit")
	}
	if killCalls.Load() != 1 {
		t.Fatalf("hard-exit calls = %d, want 1", killCalls.Load())
	}
}

func TestStartDaemonDebugServerAdminEndpointsDisabledByDefault(t *testing.T) {
	t.Setenv(daemonBindAddressEnv, "127.0.0.1:0")
	t.Setenv(daemonAdminEndpointsEnv, "")

	closeServer, addr, err := startDaemonDebugServer(io.Discard, nil, nil)
	if err != nil {
		t.Fatalf("startDaemonDebugServer(): %v", err)
	}
	defer func() {
		if err := closeServer(); err != nil {
			t.Fatalf("closeServer(): %v", err)
		}
	}()

	for _, path := range []string{"/exit", "/kill"} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s status = %d body=%q, want 404", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func TestStartDaemonDebugServerExplicitBusyAddressFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	defer listener.Close()

	t.Setenv(daemonBindAddressEnv, listener.Addr().String())
	if _, _, err := startDaemonDebugServer(io.Discard, nil, nil); err == nil {
		t.Fatal("startDaemonDebugServer() error = nil, want busy-address error")
	}
}
