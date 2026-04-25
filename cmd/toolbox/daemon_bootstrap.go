package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
)

type combinedPreparedToolConsumer []toolsetctl.PreparedToolConsumer

var ensureSessionDaemon = func(mode, cwd string, stderr io.Writer) (daemon.SessionDelegate, error) {
	delegate, err := daemon.OpenSessionDelegate(mode, cwd, stderr)
	if err == nil {
		return delegate, nil
	}
	if errors.Is(err, daemon.ErrUnsupportedPlatform) {
		return daemon.NewNoopSessionDelegate(), nil
	}
	return nil, err
}

func (c combinedPreparedToolConsumer) SetPreparedTools(prepared toolset.PreparedToolset) {
	for _, consumer := range c {
		if consumer == nil {
			continue
		}
		consumer.SetPreparedTools(prepared)
	}
}

func bindSecretEpochReload(delegate daemon.SessionDelegate, stderr io.Writer, reload func() error) {
	if delegate == nil || reload == nil {
		return
	}

	var (
		mu      sync.Mutex
		running bool
		pending bool
	)

	delegate.SetSecretEpochHandler(func() {
		mu.Lock()
		if running {
			pending = true
			mu.Unlock()
			return
		}
		running = true
		mu.Unlock()

		go func() {
			for {
				if err := reload(); err != nil && stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon reload error: %v\n", err)
				}

				mu.Lock()
				if !pending {
					running = false
					mu.Unlock()
					return
				}
				pending = false
				mu.Unlock()
			}
		}()
	})
}

type pendingApprovalProvider interface {
	PendingApprovals(context.Context) ([]codemodesession.PendingApproval, error)
}

type approvalActionExecutor interface {
	ApplyApprovals(context.Context, []codemodesession.ApprovalDecision) error
}

type sessionBindingProvider interface {
	BoundTBSession() string
	Locked() bool
}

func syncPendingApprovals(ctx context.Context, provider pendingApprovalProvider, delegate daemon.SessionDelegate, stderr io.Writer) error {
	if provider == nil || delegate == nil {
		return nil
	}
	approvals, err := provider.PendingApprovals(ctx)
	if err != nil {
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "toolbox daemon approval sync error: %v\n", err)
		}
		return err
	}
	delegate.SetPendingApprovals(toDaemonApprovals(approvals))
	return nil
}

func bindApprovalExecution(delegate daemon.SessionDelegate, stderr io.Writer, executor approvalActionExecutor, refresh func() error) {
	if delegate == nil || executor == nil {
		return
	}
	delegate.SetApprovalHandler(func(decision daemon.ApprovalDecision) {
		go func() {
			approved := false
			switch decision.Action {
			case daemon.ApprovalActionApprove:
				approved = true
			case daemon.ApprovalActionReject:
			default:
				err := fmt.Errorf("unknown approval action %q", decision.Action)
				if stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon approval error: %v\n", err)
				}
				return
			}
			err := executor.ApplyApprovals(context.Background(), []codemodesession.ApprovalDecision{{
				ToolCallID: decision.ToolCallID,
				Approved:   approved,
				Reason:     decision.Message,
			}})
			if err != nil {
				if stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon approval error: %v\n", err)
				}
				return
			}
			if refresh != nil {
				if err := refresh(); err != nil && stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon approval sync error: %v\n", err)
				}
			}
		}()
	})
}

func setSessionBinding(delegate daemon.SessionDelegate, boundTBSession string, locked bool) {
	if delegate == nil {
		return
	}
	delegate.SetSessionBinding(boundTBSession, locked)
}

func syncSessionBinding(delegate daemon.SessionDelegate, provider sessionBindingProvider) {
	if delegate == nil || provider == nil {
		return
	}
	delegate.SetSessionBinding(provider.BoundTBSession(), provider.Locked())
}

func toDaemonApprovals(approvals []codemodesession.PendingApproval) []daemon.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]daemon.PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		next := daemon.PendingApprovalSnapshot{
			ToolCallID:    approval.ToolCallID,
			TBSession:     approval.TBSession,
			ToolName:      approval.ToolName,
			ParamsInspect: approval.ParamsInspect,
			EffectID:      approval.EffectID,
			Status:        approval.Status,
			Error:         approval.Error,
		}
		out = append(out, next)
	}
	return out
}
