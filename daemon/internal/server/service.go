package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"sync/atomic"

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

	if err := stream.Send(s.currentStateUpdate(0)); err != nil {
		return err
	}

	var clientID atomic.Uint64
	recvErrCh := make(chan error, 1)
	go func() {
		defer func() {
			if id := clientID.Load(); id != 0 {
				s.registry.RemoveClient(id)
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
			id := clientID.Load()
			if id == 0 {
				id = s.registry.AddClient(int(msg.GetPid()))
				clientID.Store(id)
			}
			s.registry.UpdateClient(id, SessionState{
				Mode:             msg.GetMode(),
				Locked:           msg.GetLocked(),
				BoundTBSession:   msg.GetBoundTbSession(),
				WorkingDir:       msg.GetWorkingDir(),
				PreparedTools:    append([]string(nil), msg.GetPreparedTools()...),
				PendingApprovals: pendingApprovalsFromProto(msg.GetPendingApprovals()),
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
			if err := stream.Send(s.currentStateUpdate(clientID.Load())); err != nil {
				return err
			}
		}
	}
}

func (s *SessionService) currentStateUpdate(clientID uint64) *daemonv1.StateUpdate {
	if s == nil {
		return &daemonv1.StateUpdate{}
	}
	return &daemonv1.StateUpdate{
		Clients:           clientSnapshotsToProto(s.registry.Clients()),
		SecretEpoch:       s.secretEpoch.Current(),
		ApprovalDecisions: approvalDecisionsToProto(s.registry.ApprovalDecisions(clientID)),
	}
}

func clientSnapshotsToProto(clients []ClientSnapshot) []*daemonv1.ClientSnapshot {
	if len(clients) == 0 {
		return nil
	}
	out := make([]*daemonv1.ClientSnapshot, 0, len(clients))
	for _, client := range clients {
		snapshot := &daemonv1.ClientSnapshot{
			Pid:              int32(client.PID),
			Mode:             client.Mode,
			Locked:           client.Locked,
			BoundTbSession:   client.BoundTBSession,
			WorkingDir:       client.WorkingDir,
			PreparedTools:    append([]string(nil), client.PreparedTools...),
			PendingApprovals: pendingApprovalsToProto(client.PendingApprovals),
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

func pendingApprovalsFromProto(approvals []*daemonv1.PendingApprovalSnapshot) []PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		next := PendingApprovalSnapshot{
			ToolCallID:    approval.GetToolCallId(),
			TBSession:     approval.GetTbSession(),
			ToolName:      approval.GetToolName(),
			ParamsInspect: approval.GetParamsInspect(),
			EffectID:      approval.GetEffectId(),
			Status:        approval.GetStatus(),
			Error:         approval.GetError(),
		}
		out = append(out, next)
	}
	return out
}

func pendingApprovalsToProto(approvals []PendingApprovalSnapshot) []*daemonv1.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]*daemonv1.PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		next := &daemonv1.PendingApprovalSnapshot{
			ToolCallId:    approval.ToolCallID,
			TbSession:     approval.TBSession,
			ToolName:      approval.ToolName,
			ParamsInspect: approval.ParamsInspect,
			EffectId:      approval.EffectID,
			Status:        approval.Status,
			Error:         approval.Error,
		}
		out = append(out, next)
	}
	return out
}

func approvalDecisionsToProto(decisions []ApprovalDecision) []*daemonv1.ApprovalDecision {
	if len(decisions) == 0 {
		return nil
	}
	out := make([]*daemonv1.ApprovalDecision, 0, len(decisions))
	for _, decision := range decisions {
		out = append(out, &daemonv1.ApprovalDecision{
			Action:     decision.Action,
			ToolCallId: decision.ToolCallID,
			Message:    decision.Message,
		})
	}
	return out
}
