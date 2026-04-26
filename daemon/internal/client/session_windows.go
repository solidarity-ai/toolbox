//go:build windows

package client

import daemonserver "github.com/solidarity-ai/toolbox/daemon/internal/server"

type SessionRegistration struct{}

func OpenSessionRegistration(daemonserver.SessionState) (*SessionRegistration, error) {
	return nil, ErrUnsupportedPlatform
}

func (r *SessionRegistration) Update(daemonserver.SessionState) error {
	return ErrUnsupportedPlatform
}

func (r *SessionRegistration) SetSecretEpochHandler(func()) {}

func (r *SessionRegistration) SetApprovalBatchHandler(func([]daemonserver.ApprovalDecision)) {}

func (r *SessionRegistration) currentState() daemonserver.SessionState {
	return daemonserver.SessionState{}
}

func (r *SessionRegistration) Close() error {
	return ErrUnsupportedPlatform
}
