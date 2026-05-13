package client

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	daemonplatform "github.com/solidarity-ai/toolbox/daemon/internal/processctl/platform"
	daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

const sessionServiceBaseURL = "http://toolbox-daemon"

var ErrUnsupportedPlatform = daemonplatform.ErrUnsupportedPlatform

type Client struct {
	mu                 sync.Mutex
	socketPath         string
	httpClient         *http.Client
	sessionService     daemonv1connect.SessionServiceClient
	secretStoreService daemonv1connect.SecretStoreServiceClient
}

type PingResult struct {
	Payload string
	PID     int
}

type PendingApprovalSnapshot = daemonserver.PendingApprovalSnapshot
type ApprovalDecision = daemonserver.ApprovalDecision

type SessionDelegate interface {
	toolsetctl.PreparedToolConsumer
	SetSessionBinding(boundTBSession string, locked bool)
	SetSessionIntent(text, source, updatedAt string)
	SetPendingApprovals([]PendingApprovalSnapshot)
	SetApprovalBatchHandler(func([]ApprovalDecision))
	SetSecretEpochHandler(func())
	Close() error
}

type sessionDelegate struct {
	reg    *SessionRegistration
	mode   string
	cwd    string
	stderr io.Writer
}

type noopSessionDelegate struct{}

type SecretStore struct {
	unlockKey        string
	backupCodeWriter io.Writer
}

func OpenSessionDelegate(mode, cwd string, stderr io.Writer) (SessionDelegate, error) {
	reg, err := OpenSessionRegistration(daemonserver.SessionState{
		Mode:       mode,
		WorkingDir: cwd,
	})
	if err != nil {
		return nil, err
	}
	return &sessionDelegate{
		reg:    reg,
		mode:   mode,
		cwd:    cwd,
		stderr: stderr,
	}, nil
}

func NewNoopSessionDelegate() SessionDelegate {
	return noopSessionDelegate{}
}

func NewSecretStore(unlockKey string) *SecretStore {
	return &SecretStore{unlockKey: unlockKey}
}

func NewSecretStoreWithBackupCodeWriter(unlockKey string, w io.Writer) *SecretStore {
	return &SecretStore{unlockKey: unlockKey, backupCodeWriter: w}
}

func (d *sessionDelegate) SetPreparedTools(prepared toolset.PreparedToolset) {
	if d == nil || d.reg == nil {
		return
	}
	state := d.reg.currentState()
	state.Mode = d.mode
	state.WorkingDir = d.cwd
	state.PreparedTools = toolset.PreparedToolRefs(prepared)
	if err := d.reg.Update(state); err != nil && d.stderr != nil {
		_, _ = fmt.Fprintf(d.stderr, "toolbox daemon sync error: %v\n", err)
	}
}

func (d *sessionDelegate) SetSessionBinding(boundTBSession string, locked bool) {
	if d == nil || d.reg == nil {
		return
	}
	state := d.reg.currentState()
	state.Mode = d.mode
	state.WorkingDir = d.cwd
	state.Locked = locked
	state.BoundTBSession = boundTBSession
	if err := d.reg.Update(state); err != nil && d.stderr != nil {
		_, _ = fmt.Fprintf(d.stderr, "toolbox daemon sync error: %v\n", err)
	}
}

func (d *sessionDelegate) SetSessionIntent(text, source, updatedAt string) {
	if d == nil || d.reg == nil {
		return
	}
	state := d.reg.currentState()
	state.Mode = d.mode
	state.WorkingDir = d.cwd
	state.IntentText = text
	state.IntentSource = source
	state.IntentUpdatedAt = updatedAt
	if err := d.reg.Update(state); err != nil && d.stderr != nil {
		_, _ = fmt.Fprintf(d.stderr, "toolbox daemon sync error: %v\n", err)
	}
}

func (d *sessionDelegate) SetPendingApprovals(approvals []PendingApprovalSnapshot) {
	if d == nil || d.reg == nil {
		return
	}
	state := d.reg.currentState()
	state.Mode = d.mode
	state.WorkingDir = d.cwd
	state.PendingApprovals = append([]PendingApprovalSnapshot(nil), approvals...)
	if err := d.reg.Update(state); err != nil && d.stderr != nil {
		_, _ = fmt.Fprintf(d.stderr, "toolbox daemon sync error: %v\n", err)
	}
}

func (d *sessionDelegate) Close() error {
	if d == nil || d.reg == nil {
		return nil
	}
	return d.reg.Close()
}

func (d *sessionDelegate) SetSecretEpochHandler(fn func()) {
	if d == nil || d.reg == nil {
		return
	}
	d.reg.SetSecretEpochHandler(fn)
}

func (d *sessionDelegate) SetApprovalBatchHandler(fn func([]ApprovalDecision)) {
	if d == nil || d.reg == nil {
		return
	}
	d.reg.SetApprovalBatchHandler(fn)
}

func (noopSessionDelegate) SetPreparedTools(toolset.PreparedToolset) {}

func (noopSessionDelegate) SetSessionBinding(string, bool) {}

func (noopSessionDelegate) SetPendingApprovals([]PendingApprovalSnapshot) {}
func (noopSessionDelegate) SetSessionIntent(string, string, string)       {}

func (noopSessionDelegate) SetSecretEpochHandler(func()) {}

func (noopSessionDelegate) SetApprovalBatchHandler(func([]ApprovalDecision)) {}

func (noopSessionDelegate) Close() error { return nil }
