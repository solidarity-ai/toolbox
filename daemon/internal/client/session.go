//go:build !windows

package client

import (
	"context"
	"errors"
	"os"
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
		if _, err := stream.Receive(); err != nil {
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
	return cloned
}

func (r *SessionRegistration) ensureStream(state daemonserver.SessionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stream != nil {
		return nil
	}

	stream := r.client.SyncState(context.Background())
	if err := stream.Send(sessionStateToProto(state)); err != nil {
		_ = stream.CloseResponse()
		return err
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
		Pid:           int32(os.Getpid()),
		Mode:          state.Mode,
		WorkingDir:    state.WorkingDir,
		PreparedTools: append([]string(nil), state.PreparedTools...),
	}
}
