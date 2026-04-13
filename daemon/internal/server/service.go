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

	registry *Registry
}

func NewSessionService(registry *Registry) *SessionService {
	if registry == nil {
		registry = NewRegistry()
	}
	return &SessionService{registry: registry}
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
	var clientID uint64
	defer func() {
		if clientID != 0 {
			s.registry.RemoveClient(clientID)
		}
	}()

	for {
		msg, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if clientID == 0 {
			clientID = s.registry.AddClient(int(msg.GetPid()))
		}
		s.registry.UpdateClient(clientID, SessionState{
			Mode:          msg.GetMode(),
			WorkingDir:    msg.GetWorkingDir(),
			PreparedTools: append([]string(nil), msg.GetPreparedTools()...),
		})
		if err := stream.Send(&daemonv1.StateUpdate{
			Clients: clientSnapshotsToProto(s.registry.Clients()),
		}); err != nil {
			return err
		}
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
