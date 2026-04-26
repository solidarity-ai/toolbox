//go:build !windows

package client

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"
	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

const sessionHeartbeatInterval = 10 * time.Second

type SessionRegistration struct {
	socketPath string
	client     daemonv1connect.SessionServiceClient
	stream     *connect.BidiStreamForClient[daemonv1.SessionState, daemonv1.StateUpdate]

	mu    sync.RWMutex
	state daemonserver.SessionState

	secretEpoch              string
	pendingSecretEpochChange bool
	onSecretEpochChange      func()
	onApprovalBatch          func([]daemonserver.ApprovalDecision)
	pendingApprovals         []daemonserver.ApprovalDecision
	seenApprovals            map[string]struct{}

	done chan struct{}
	wg   sync.WaitGroup
}

func OpenSessionRegistration(state daemonserver.SessionState) (*SessionRegistration, error) {
	client, err := EnsureConnection()
	if err != nil {
		return nil, err
	}
	_ = client.Close()

	reg := &SessionRegistration{
		socketPath: client.socketPath,
		client:     daemonv1connect.NewSessionServiceClient(transport.NewUnixHTTPClient(client.socketPath), sessionServiceBaseURL),
		state:      cloneSessionState(state),
		done:       make(chan struct{}),
	}
	if err := reg.ensureStream(reg.state); err != nil {
		return nil, err
	}

	reg.wg.Add(1)
	go reg.heartbeat()
	reg.wg.Add(1)
	go reg.receiveLoop()
	return reg, nil
}

func (r *SessionRegistration) Update(state daemonserver.SessionState) error {
	if r == nil {
		return nil
	}

	state = cloneSessionState(state)
	r.mu.Lock()
	r.state = state
	r.mu.Unlock()
	return r.sync(state)
}

func (r *SessionRegistration) Close() error {
	if r == nil {
		return nil
	}

	select {
	case <-r.done:
	default:
		close(r.done)
	}
	r.mu.Lock()
	if r.stream != nil {
		_ = r.stream.CloseRequest()
		_ = r.stream.CloseResponse()
		r.stream = nil
	}
	r.mu.Unlock()
	r.wg.Wait()
	return nil
}

func (r *SessionRegistration) SetSecretEpochHandler(fn func()) {
	if r == nil {
		return
	}

	var callback func()
	r.mu.Lock()
	r.onSecretEpochChange = fn
	if fn != nil && r.pendingSecretEpochChange {
		r.pendingSecretEpochChange = false
		callback = fn
	}
	r.mu.Unlock()

	if callback != nil {
		callback()
	}
}

func (r *SessionRegistration) SetApprovalBatchHandler(fn func([]daemonserver.ApprovalDecision)) {
	if r == nil {
		return
	}

	var pending []daemonserver.ApprovalDecision
	r.mu.Lock()
	r.onApprovalBatch = fn
	if fn != nil && len(r.pendingApprovals) > 0 {
		pending = append([]daemonserver.ApprovalDecision(nil), r.pendingApprovals...)
		r.pendingApprovals = nil
	}
	r.mu.Unlock()

	if fn != nil && len(pending) > 0 {
		fn(pending)
	}
}

func (r *SessionRegistration) heartbeat() {
	defer r.wg.Done()

	ticker := time.NewTicker(sessionHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			_ = r.sync(r.currentState())
		}
	}
}

func (r *SessionRegistration) receiveLoop() {
	defer r.wg.Done()

	for {
		stream, ok := r.currentStream()
		if !ok {
			select {
			case <-r.done:
				return
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		update, err := stream.Receive()
		if err == nil {
			r.handleStateUpdate(update)
			continue
		}
		if err != nil {
			r.clearStream(stream)
			if errors.Is(err, context.Canceled) {
				return
			}
			select {
			case <-r.done:
				return
			default:
			}
		}
	}
}

func (r *SessionRegistration) handleStateUpdate(update *daemonv1.StateUpdate) {
	if r == nil || update == nil {
		return
	}

	epoch := strings.TrimSpace(update.GetSecretEpoch())
	var callback func()
	if epoch != "" {
		r.mu.Lock()
		switch {
		case r.secretEpoch == "":
			r.secretEpoch = epoch
		case r.secretEpoch != epoch:
			r.secretEpoch = epoch
			if r.onSecretEpochChange != nil {
				callback = r.onSecretEpochChange
			} else {
				r.pendingSecretEpochChange = true
			}
		}
		r.mu.Unlock()
	}

	if callback != nil {
		callback()
	}

	r.handleApprovalDecisions(update.GetApprovalDecisions())
}

func (r *SessionRegistration) currentState() daemonserver.SessionState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSessionState(r.state)
}

func (r *SessionRegistration) sync(state daemonserver.SessionState) error {
	if err := r.ensureStream(state); err != nil {
		return err
	}

	r.mu.RLock()
	stream := r.stream
	r.mu.RUnlock()
	if stream == nil {
		return errors.New("session stream is not available")
	}
	if err := stream.Send(sessionStateToProto(state)); err == nil {
		return nil
	}

	r.clearStream(stream)
	if err := r.ensureStream(state); err != nil {
		return err
	}
	r.mu.RLock()
	stream = r.stream
	r.mu.RUnlock()
	if stream == nil {
		return errors.New("session stream is not available after reconnect")
	}
	return stream.Send(sessionStateToProto(state))
}

func cloneSessionState(state daemonserver.SessionState) daemonserver.SessionState {
	cloned := state
	cloned.PreparedTools = append([]string(nil), state.PreparedTools...)
	cloned.PendingApprovals = clonePendingApprovals(state.PendingApprovals)
	return cloned
}

func (r *SessionRegistration) ensureStream(state daemonserver.SessionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stream != nil {
		return nil
	}

	if r.client == nil {
		client, err := EnsureConnection()
		if err != nil {
			return err
		}
		r.socketPath = client.socketPath
		r.client = daemonv1connect.NewSessionServiceClient(transport.NewUnixHTTPClient(client.socketPath), sessionServiceBaseURL)
		_ = client.Close()
	}

	stream := r.client.SyncState(context.Background())
	if err := stream.Send(sessionStateToProto(state)); err != nil {
		_ = stream.CloseResponse()
		client, reconnectErr := EnsureConnection()
		if reconnectErr != nil {
			return errors.Join(err, reconnectErr)
		}
		r.socketPath = client.socketPath
		r.client = daemonv1connect.NewSessionServiceClient(transport.NewUnixHTTPClient(client.socketPath), sessionServiceBaseURL)
		_ = client.Close()
		stream = r.client.SyncState(context.Background())
		if retryErr := stream.Send(sessionStateToProto(state)); retryErr != nil {
			_ = stream.CloseResponse()
			return retryErr
		}
	}
	r.stream = stream
	return nil
}

func (r *SessionRegistration) currentStream() (*connect.BidiStreamForClient[daemonv1.SessionState, daemonv1.StateUpdate], bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stream, r.stream != nil
}

func (r *SessionRegistration) clearStream(stream *connect.BidiStreamForClient[daemonv1.SessionState, daemonv1.StateUpdate]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stream != stream {
		return
	}
	_ = r.stream.CloseResponse()
	r.stream = nil
}

func sessionStateToProto(state daemonserver.SessionState) *daemonv1.SessionState {
	return &daemonv1.SessionState{
		Pid:              int32(os.Getpid()),
		Mode:             state.Mode,
		Locked:           state.Locked,
		BoundTbSession:   state.BoundTBSession,
		IntentText:       state.IntentText,
		IntentSource:     state.IntentSource,
		IntentUpdatedAt:  state.IntentUpdatedAt,
		WorkingDir:       state.WorkingDir,
		PreparedTools:    append([]string(nil), state.PreparedTools...),
		PendingApprovals: pendingApprovalsToProto(state.PendingApprovals),
	}
}

func (r *SessionRegistration) handleApprovalDecisions(decisions []*daemonv1.ApprovalDecision) {
	if r == nil || len(decisions) == 0 {
		return
	}

	var pending []daemonserver.ApprovalDecision
	var batchCallback func([]daemonserver.ApprovalDecision)
	r.mu.Lock()
	if r.seenApprovals == nil {
		r.seenApprovals = make(map[string]struct{})
	}
	for _, decision := range decisions {
		next := daemonserver.ApprovalDecision{
			Action:           strings.TrimSpace(decision.GetAction()),
			ToolCallID:       strings.TrimSpace(decision.GetToolCallId()),
			Message:          decision.GetMessage(),
			ClientDecisionID: strings.TrimSpace(decision.GetClientDecisionId()),
		}
		key := approvalDecisionKey(next)
		if key == "" {
			continue
		}
		if _, seen := r.seenApprovals[key]; seen {
			continue
		}
		r.seenApprovals[key] = struct{}{}
		if r.onApprovalBatch != nil {
			pending = append(pending, next)
		} else {
			r.pendingApprovals = append(r.pendingApprovals, next)
		}
	}
	batchCallback = r.onApprovalBatch
	r.mu.Unlock()

	if batchCallback != nil && len(pending) > 0 {
		batchCallback(pending)
	}
}

func clonePendingApprovals(approvals []daemonserver.PendingApprovalSnapshot) []daemonserver.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]daemonserver.PendingApprovalSnapshot, len(approvals))
	copy(out, approvals)
	return out
}

func pendingApprovalsToProto(approvals []daemonserver.PendingApprovalSnapshot) []*daemonv1.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]*daemonv1.PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		next := &daemonv1.PendingApprovalSnapshot{
			ToolCallId:      approval.ToolCallID,
			TbSession:       approval.TBSession,
			IntentText:      approval.IntentText,
			IntentSource:    approval.IntentSource,
			IntentUpdatedAt: approval.IntentUpdatedAt,
			ToolName:        approval.ToolName,
			FullToolName:    approval.FullToolName,
			PackageKey:      approval.PackageKey,
			PackageLabel:    approval.PackageLabel,
			ToolLabel:       approval.ToolLabel,
			Description:     approval.Description,
			ParamsInspect:   approval.ParamsInspect,
			Presentation:    approval.Presentation,
			EffectId:        approval.EffectID,
			CellId:          approval.CellID,
			Status:          approval.Status,
			Error:           approval.Error,
			CreatedAt:       approval.CreatedAt,
			UpdatedAt:       approval.UpdatedAt,
		}
		out = append(out, next)
	}
	return out
}

func approvalDecisionKey(decision daemonserver.ApprovalDecision) string {
	action := strings.TrimSpace(decision.Action)
	toolCallID := strings.TrimSpace(decision.ToolCallID)
	if action == "" || toolCallID == "" {
		return ""
	}
	return action + "\n" + toolCallID + "\n" + decision.Message
}
