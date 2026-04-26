package server

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/solidarity-ai/toolbox/secrets"

	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

type Server struct {
	listener       net.Listener
	http           *http.Server
	registry       *Registry
	sessionService *SessionService
	secretService  *SecretStoreService
}

func NewServer(listener net.Listener) *Server {
	return NewServerWithRegistryAndSecretStore(listener, nil, nil)
}

func NewServerWithRegistry(listener net.Listener, registry *Registry) *Server {
	return NewServerWithRegistryAndSecretStore(listener, registry, nil)
}

func NewServerWithRegistryAndSecretStore(listener net.Listener, registry *Registry, store secrets.ManagedSecretStore) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	secretEpoch := NewSecretEpoch()
	notifier := newStateNotifier()
	sessionService := newSessionService(registry, secretEpoch, notifier)
	secretService := newSecretStoreService(store, secretEpoch, notifier)
	mux := http.NewServeMux()
	path, handler := sessionService.Handler()
	mux.Handle(path, handler)
	path, handler = secretService.Handler()
	mux.Handle(path, handler)
	httpServer := &http.Server{Handler: mux}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpServer.Protocols = protocols
	return &Server{
		listener:       listener,
		http:           httpServer,
		registry:       registry,
		sessionService: sessionService,
		secretService:  secretService,
	}
}

func (s *Server) Serve() error {
	err := s.http.Serve(transport.NewVerifiedListener(s.listener))
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) Close() error {
	err := s.http.Close()
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) Clients() []ClientSnapshot {
	return s.registry.Clients()
}

func (s *Server) PendingApprovals() []PendingApprovalSnapshot {
	if s == nil || s.registry == nil {
		return nil
	}
	return s.registry.PendingApprovals()
}

func (s *Server) Revision() uint64 {
	if s == nil || s.registry == nil {
		return 0
	}
	return s.registry.Revision()
}

func (s *Server) Registry() *Registry {
	if s == nil {
		return nil
	}
	return s.registry
}

func (s *Server) UnlockSecretStore(ctx context.Context, unlockKey string) error {
	if s == nil || s.secretService == nil {
		return nil
	}
	return s.secretService.UnlockStore(ctx, unlockKey)
}

func (s *Server) LockSecretStore(ctx context.Context) error {
	if s == nil || s.secretService == nil {
		return nil
	}
	return s.secretService.LockStore(ctx)
}

func (s *Server) SecretStoreLocked(ctx context.Context) (bool, error) {
	if s == nil || s.secretService == nil {
		return true, nil
	}
	return s.secretService.Locked(ctx)
}

func (s *Server) ApplyApprovals(_ context.Context, decisions []ApprovalDecision) error {
	if s == nil || s.registry == nil {
		return nil
	}
	for _, decision := range decisions {
		s.registry.QueueApprovalDecision(decision)
	}
	if s.sessionService != nil && s.sessionService.notifier != nil {
		s.sessionService.notifier.Notify()
	}
	return nil
}
