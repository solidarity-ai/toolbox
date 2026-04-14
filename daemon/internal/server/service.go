package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SessionService struct {
	daemonv1connect.UnimplementedSessionServiceHandler

	registry    *Registry
	secretEpoch *SecretEpoch
	notifier    *stateNotifier
}

func NewSessionService(registry *Registry) *SessionService {
	return newSessionService(registry, NewSecretEpoch(), newStateNotifier())
}

func newSessionService(registry *Registry, secretEpoch *SecretEpoch, notifier *stateNotifier) *SessionService {
	if registry == nil {
		registry = NewRegistry()
	}
	if secretEpoch == nil {
		secretEpoch = NewSecretEpoch()
	}
	if notifier == nil {
		notifier = newStateNotifier()
	}
	return &SessionService{
		registry:    registry,
		secretEpoch: secretEpoch,
		notifier:    notifier,
	}
}

func (s *SessionService) Handler(opts ...connect.HandlerOption) (string, http.Handler) {
	return daemonv1connect.NewSessionServiceHandler(s, opts...)
}

func (s *SessionService) Clients() []ClientSnapshot {
	if s == nil {
		return nil
	}
	return s.registry.Clients()
}

func (s *SessionService) Ping(context.Context, *connect.Request[daemonv1.PingRequest]) (*connect.Response[daemonv1.PingResponse], error) {
	return connect.NewResponse(&daemonv1.PingResponse{
		Payload: "pong",
		Pid:     int32(os.Getpid()),
	}), nil
}

func (s *SessionService) SyncState(_ context.Context, stream *connect.BidiStream[daemonv1.SessionState, daemonv1.StateUpdate]) error {
	subID, updates := s.notifier.Subscribe()
	defer s.notifier.Unsubscribe(subID)

	if err := stream.Send(s.currentStateUpdate()); err != nil {
		return err
	}

	recvErrCh := make(chan error, 1)
	go func() {
		var clientID uint64
		defer func() {
			if clientID != 0 {
				s.registry.RemoveClient(clientID)
				s.notifier.Notify()
			}
		}()

		for {
			msg, err := stream.Receive()
			if err != nil {
				switch {
				case errors.Is(err, io.EOF), errors.Is(err, context.Canceled):
					recvErrCh <- nil
				default:
					recvErrCh <- err
				}
				return
			}
			if clientID == 0 {
				clientID = s.registry.AddClient(int(msg.GetPid()))
			}
			s.registry.UpdateClient(clientID, SessionState{
				Mode:          msg.GetMode(),
				WorkingDir:    msg.GetWorkingDir(),
				PreparedTools: append([]string(nil), msg.GetPreparedTools()...),
			})
			s.notifier.Notify()
		}
	}()

	for {
		select {
		case err := <-recvErrCh:
			return err
		case _, ok := <-updates:
			if !ok {
				return nil
			}
			if err := stream.Send(s.currentStateUpdate()); err != nil {
				return err
			}
		}
	}
}

func (s *SessionService) currentStateUpdate() *daemonv1.StateUpdate {
	if s == nil {
		return &daemonv1.StateUpdate{}
	}
	return &daemonv1.StateUpdate{
		Clients:     clientSnapshotsToProto(s.registry.Clients()),
		SecretEpoch: s.secretEpoch.Current(),
	}
}

func clientSnapshotsToProto(clients []ClientSnapshot) []*daemonv1.ClientSnapshot {
	if len(clients) == 0 {
		return nil
	}
	out := make([]*daemonv1.ClientSnapshot, 0, len(clients))
	for _, client := range clients {
		snapshot := &daemonv1.ClientSnapshot{
			Pid:           int32(client.PID),
			Mode:          client.Mode,
			WorkingDir:    client.WorkingDir,
			PreparedTools: append([]string(nil), client.PreparedTools...),
		}
		if !client.ConnectedAt.IsZero() {
			snapshot.ConnectedAt = timestamppb.New(client.ConnectedAt)
		}
		if !client.LastSyncAt.IsZero() {
			snapshot.LastSyncAt = timestamppb.New(client.LastSyncAt)
		}
		out = append(out, snapshot)
	}
	return out
}
