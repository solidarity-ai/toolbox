package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"time"

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

type sessionIntentProvider interface {
	Intent() (text, source string, updatedAt time.Time)
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
	delegate.SetApprovalBatchHandler(func(decisions []daemon.ApprovalDecision) {
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil && stderr != nil {
					_, _ = fmt.Fprintf(stderr, "toolbox daemon approval panic applying %d decision(s): %v\n%s", len(decisions), recovered, debug.Stack())
				}
			}()

			batch := make([]codemodesession.ApprovalDecision, 0, len(decisions))
			for _, decision := range decisions {
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
				batch = append(batch, codemodesession.ApprovalDecision{
					ToolCallID: decision.ToolCallID,
					Approved:   approved,
					Reason:     decision.Message,
				})
			}
			err := executor.ApplyApprovals(context.Background(), batch)
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

func syncSessionIntent(delegate daemon.SessionDelegate, provider sessionIntentProvider) {
	if delegate == nil || provider == nil {
		return
	}
	text, source, updatedAt := provider.Intent()
	updated := ""
	if !updatedAt.IsZero() {
		updated = updatedAt.Format(time.RFC3339Nano)
	}
	delegate.SetSessionIntent(text, source, updated)
}

func toDaemonApprovals(approvals []codemodesession.PendingApproval) []daemon.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]daemon.PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		next := daemon.PendingApprovalSnapshot{
			ToolCallID:          approval.ToolCallID,
			TBSession:           approval.TBSession,
			IntentText:          approval.IntentText,
			IntentSource:        approval.IntentSource,
			IntentUpdatedAt:     formatApprovalTime(approval.IntentUpdatedAt),
			ToolName:            approval.ToolName,
			FullToolName:        approval.FullToolName,
			PackageKey:          approval.PackageKey,
			PackageLabel:        approval.PackageLabel,
			ToolLabel:           approval.ToolLabel,
			Description:         approval.Description,
			RequiresCredentials: approval.RequiresCredentials,
			ParamsInspect:       approval.ParamsInspect,
			Presentation:        string(approval.Presentation),
			EffectID:            approval.EffectID,
			CellID:              approval.CellID,
			Status:              approval.Status,
			Error:               approval.Error,
			CreatedAt:           formatApprovalTime(approval.CreatedAt),
			UpdatedAt:           formatApprovalTime(approval.UpdatedAt),
		}
		out = append(out, next)
	}
	return out
}

func formatApprovalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}
