package server

import (
	"errors"
	"net"
	"net/http"

	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

type Server struct {
	listener net.Listener
	http     *http.Server
	registry *Registry
}

func NewServer(listener net.Listener) *Server {
	return NewServerWithRegistry(listener, nil)
}

func NewServerWithRegistry(listener net.Listener, registry *Registry) *Server {
	if registry == nil {
		registry = NewRegistry()
	}
	service := NewSessionService(registry)
	mux := http.NewServeMux()
	path, handler := service.Handler()
	mux.Handle(path, handler)
	httpServer := &http.Server{Handler: mux}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpServer.Protocols = protocols
	return &Server{
		listener: listener,
		http:     httpServer,
		registry: registry,
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

func (s *Server) Registry() *Registry {
	if s == nil {
		return nil
	}
	return s.registry
}
