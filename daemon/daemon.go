package daemon

import (
	"errors"
	"io"
	"net"

	daemonclient "github.com/solidarity-ai/toolbox/daemon/internal/client"
	daemonprocessctl "github.com/solidarity-ai/toolbox/daemon/internal/processctl"
	daemonpaths "github.com/solidarity-ai/toolbox/daemon/internal/processctl/paths"
	daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"
	daemontransport "github.com/solidarity-ai/toolbox/daemon/internal/transport"
	"github.com/solidarity-ai/toolbox/secrets"
)

var ErrUnsupportedPlatform = daemonclient.ErrUnsupportedPlatform

type Client = daemonclient.Client
type PingResult = daemonclient.PingResult
type SessionRegistration = daemonclient.SessionRegistration
type SessionDelegate = daemonclient.SessionDelegate

type SessionState = daemonserver.SessionState
type ClientSnapshot = daemonserver.ClientSnapshot
type PendingApprovalSnapshot = daemonserver.PendingApprovalSnapshot
type ApprovalDecision = daemonserver.ApprovalDecision
type Registry = daemonserver.Registry
type Server = daemonserver.Server
type SessionService = daemonserver.SessionService
type SecretStore = daemonclient.SecretStore
type SecretStoreService = daemonserver.SecretStoreService

const (
	ApprovalActionApprove = daemonserver.ApprovalActionApprove
	ApprovalActionReject  = daemonserver.ApprovalActionReject
)

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

func StopAll() ([]int, error) {
	return daemonprocessctl.StopAll()
}

func StopAllWithProgress(logf func(string, ...any)) ([]int, error) {
	return daemonprocessctl.StopAllWithProgress(logf)
}

func OpenSessionRegistration(state SessionState) (*SessionRegistration, error) {
	return daemonclient.OpenSessionRegistration(state)
}

func OpenSessionDelegate(mode, cwd string, stderr io.Writer) (SessionDelegate, error) {
	return daemonclient.OpenSessionDelegate(mode, cwd, stderr)
}

func NewSecretStore(unlockKey string) *SecretStore {
	return daemonclient.NewSecretStore(unlockKey)
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

func NewSecretStoreService(store secrets.ManagedSecretStore) *SecretStoreService {
	return daemonserver.NewSecretStoreService(store)
}

func NewServer(listener net.Listener) *Server {
	return daemonserver.NewServer(listener)
}

func NewServerWithRegistry(listener net.Listener, registry *Registry) *Server {
	return daemonserver.NewServerWithRegistry(listener, registry)
}

func NewServerWithRegistryAndSecretStore(listener net.Listener, registry *Registry, store secrets.ManagedSecretStore) *Server {
	return daemonserver.NewServerWithRegistryAndSecretStore(listener, registry, store)
}

func NewVerifiedListener(listener net.Listener) net.Listener {
	return daemontransport.NewVerifiedListener(listener)
}
