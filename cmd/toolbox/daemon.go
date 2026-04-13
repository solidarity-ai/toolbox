package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
)

const (
	defaultDaemonBindAddress = daemon.DefaultBindAddress
	daemonBindAddressEnv     = daemon.BindAddressEnv
	daemonAdminEndpointsEnv  = "TOOLBOX_DAEMON_ENABLE_ADMIN"
)

var daemonHardExit = func() {
	proc, err := os.FindProcess(os.Getpid())
	if err == nil {
		_ = proc.Kill()
	}
}

type daemonCmd struct {
	Serve daemonServeCmd `cmd:"" name:"serve" help:"Run the daemon server."`
	Ping  daemonPingCmd  `cmd:"" name:"ping" hidden:"" help:"Ping the daemon."`
}

type daemonServeCmd struct{}

type daemonPingCmd struct {
	PID bool `name:"pid" hidden:"" help:"Include the daemon PID in the output."`
}

func runDaemonServe(stderr io.Writer) error {
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon dir: %w", err)
	}

	socketPath, err := daemon.SocketPath()
	if err != nil {
		return err
	}
	pidPath, err := daemon.PIDPath()
	if err != nil {
		return err
	}

	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on daemon socket: %w", err)
	}
	defer listener.Close()

	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("chmod daemon socket: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return fmt.Errorf("write daemon pid: %w", err)
	}

	udsServer := daemon.NewServer(listener)

	var closeDebugServer func() error
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			if closeDebugServer != nil {
				_ = closeDebugServer()
			}
			_ = udsServer.Close()
		})
	}
	closeDebugServer, _, err = startDaemonDebugServer(stderr, shutdown, udsServer.Clients)
	if err != nil {
		return err
	}

	defer func() {
		shutdown()
		_ = os.Remove(socketPath)
		_ = os.Remove(pidPath)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		shutdown()
	}()

	return udsServer.Serve()
}

func daemonBindAddress() (string, bool) {
	return daemon.BindAddress()
}

func daemonAdminEndpointsEnabled() bool {
	switch os.Getenv(daemonAdminEndpointsEnv) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	default:
		return false
	}
}

func startDaemonDebugServer(stderr io.Writer, shutdown func(), clients func() []daemon.ClientSnapshot) (func() error, string, error) {
	bindAddress, explicit := daemonBindAddress()
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) && !explicit {
			if stderr != nil {
				_, _ = fmt.Fprintf(stderr, "toolbox daemon debug server skipped: %s already in use\n", bindAddress)
			}
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("listen on daemon debug address %s: %w", bindAddress, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "pong")
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		payload := r.URL.Query().Get("payload")
		if payload == "" && r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload = string(body)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, payload)
	})
	mux.HandleFunc("/clients", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		snapshot := []daemon.ClientSnapshot(nil)
		if clients != nil {
			snapshot = clients()
		}
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	if daemonAdminEndpointsEnabled() {
		mux.HandleFunc("/exit", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "shutting down")
			if shutdown != nil {
				go shutdown()
			}
		})
		mux.HandleFunc("/kill", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "killing process")
			go func() {
				time.Sleep(50 * time.Millisecond)
				daemonHardExit()
			}()
		})
	}

	httpServer := &http.Server{Handler: mux}
	var closeOnce sync.Once
	closeServer := func() error {
		var closeErr error
		closeOnce.Do(func() {
			closeErr = httpServer.Close()
		})
		return closeErr
	}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && stderr != nil {
			_, _ = fmt.Fprintf(stderr, "toolbox daemon debug server error: %v\n", err)
		}
	}()

	return closeServer, listener.Addr().String(), nil
}

func runDaemonPing(cmd daemonPingCmd, stdout io.Writer) error {
	client, err := daemon.EnsureConnection()
	if err != nil {
		return err
	}
	defer client.Close()

	resp, err := client.Ping()
	if err != nil {
		return err
	}

	if !cmd.PID {
		_, err = fmt.Fprintln(stdout, resp.Payload)
		return err
	}

	_, err = fmt.Fprintf(stdout, "%s %d\n", resp.Payload, resp.PID)
	return err
}
