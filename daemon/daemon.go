package daemon

import (
	"errors"
	"io"
	"net"

	daemonclient "github.com/solidarity-ai/toolbox/daemon/internal/client"
	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"
	daemontransport "github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

var ErrUnsupportedPlatform = daemonclient.ErrUnsupportedPlatform

type Client = daemonclient.Client
type PingResult = daemonclient.PingResult
type SessionRegistration = daemonclient.SessionRegistration
type SessionDelegate = daemonclient.SessionDelegate

type SessionState = daemonserver.SessionState
type ClientSnapshot = daemonserver.ClientSnapshot
type Registry = daemonserver.Registry
type Server = daemonserver.Server
type SessionService = daemonserver.SessionService

func IsUnsupportedPlatform(err error) bool {
	return errors.Is(err, ErrUnsupportedPlatform)
}

func Dir() (string, error) {
	return daemonpaths.Dir()
}

func SocketPath() (string, error) {
	return daemonpaths.SocketPath()
}

func PIDPath() (string, error) {
	return daemonpaths.PIDPath()
}

func LockPath() (string, error) {
	return daemonpaths.LockPath()
}

func LogPath() (string, error) {
	return daemonpaths.LogPath()
}

func EnsureConnection() (*Client, error) {
	return daemonclient.EnsureConnection()
}

func OpenSessionRegistration(state SessionState) (*SessionRegistration, error) {
	return daemonclient.OpenSessionRegistration(state)
}

func OpenSessionDelegate(mode, cwd string, stderr io.Writer) (SessionDelegate, error) {
	return daemonclient.OpenSessionDelegate(mode, cwd, stderr)
}

func NewNoopSessionDelegate() SessionDelegate {
	return daemonclient.NewNoopSessionDelegate()
}

func NewRegistry() *Registry {
	return daemonserver.NewRegistry()
}

func NewSessionService(registry *Registry) *SessionService {
	return daemonserver.NewSessionService(registry)
}

func NewServer(listener net.Listener) *Server {
	return daemonserver.NewServer(listener)
}

func NewServerWithRegistry(listener net.Listener, registry *Registry) *Server {
	return daemonserver.NewServerWithRegistry(listener, registry)
}

func NewVerifiedListener(listener net.Listener) net.Listener {
	return daemontransport.NewVerifiedListener(listener)
}
