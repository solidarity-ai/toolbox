package codemodesession

import (
	"context"
	"errors"
	"testing"
	"time"

	repl "github.com/mackross/repljs"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestWithSubmitTimeoutAddsDefaultDeadline(t *testing.T) {
	ctx, cancel := withSubmitTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("withSubmitTimeout(context.Background()) returned context without deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 25*time.Second || remaining > 31*time.Second {
		t.Fatalf("remaining timeout = %v, want roughly %v", remaining, DefaultSubmitTimeout)
	}
}

func TestWithSubmitTimeoutPreservesExplicitDeadline(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer parentCancel()

	ctx, cancel := withSubmitTimeout(parent)
	defer cancel()

	if ctx != parent {
		t.Fatal("withSubmitTimeout should preserve caller context when it already has a deadline")
	}
}

func TestAwaitNextApprovalDoesNotLoseConcurrentPublish(t *testing.T) {
	ctx := context.Background()
	approvals := &blockingApprovalStore{
		approvals: []PendingApproval{{
			ToolCallID: "call-1",
			ToolName:   "issues.get",
		}},
		pendingEntered: make(chan struct{}),
		releasePending: make(chan struct{}),
	}
	session := &Session{
		id:             repl.SessionID("session-1"),
		approvals:      approvals,
		approvalAwaits: newApprovalAwaitDelegate(),
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	resultCh := make(chan ApprovalAwaitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := session.AwaitNextApproval(waitCtx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	select {
	case <-approvals.pendingEntered:
	case <-time.After(time.Second):
		t.Fatal("AwaitNextApproval() did not enter PendingGroups")
	}

	publishDone := make(chan struct{})
	go func() {
		session.mu.Lock()
		session.publishApprovalAwaitLocked(ApprovalAwaitResult{
			Status: ApprovalAwaitStatusApproved,
			ToolCall: PendingApproval{
				ToolCallID: "call-1",
				ToolName:   "issues.get",
			},
		})
		session.mu.Unlock()
		close(publishDone)
	}()

	close(approvals.releasePending)

	select {
	case <-publishDone:
	case <-time.After(time.Second):
		t.Fatal("publish goroutine did not complete")
	}

	select {
	case err := <-errCh:
		t.Fatalf("AwaitNextApproval() error: %v", err)
	case result := <-resultCh:
		if result.Status != ApprovalAwaitStatusApproved {
			t.Fatalf("AwaitNextApproval().Status = %q, want %q", result.Status, ApprovalAwaitStatusApproved)
		}
		if result.ToolCall.ToolCallID != "call-1" {
			t.Fatalf("AwaitNextApproval().ToolCall.ToolCallID = %q, want %q", result.ToolCall.ToolCallID, "call-1")
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitNextApproval() did not receive concurrent publish")
	}
}

type blockingApprovalStore struct {
	approvals []PendingApproval

	pendingEntered chan struct{}
	releasePending chan struct{}
}

func (s *blockingApprovalStore) BeginSubmit(repl.SessionID) {}

func (s *blockingApprovalStore) AbortSubmit(repl.SessionID) {}

func (s *blockingApprovalStore) RecordPendingToolCall(repl.SessionID, string, string, string, string, []byte) error {
	return errors.New("unexpected call")
}

func (s *blockingApprovalStore) CommitSubmit(repl.SessionID, repl.CellID) error {
	return errors.New("unexpected call")
}

func (s *blockingApprovalStore) PendingApprovals(context.Context, repl.SessionID) ([]PendingApproval, error) {
	select {
	case <-s.pendingEntered:
	default:
		close(s.pendingEntered)
	}
	<-s.releasePending
	return clonePendingApprovalsForTest(s.approvals), nil
}

func (s *blockingApprovalStore) ApplyDecisions(context.Context, repl.SessionID, []ApprovalDecision, toolset.PreparedToolset, repl.Store, toolCallJournal) ([]appliedApprovalResult, error) {
	return nil, errors.New("unexpected call")
}

func clonePendingApprovalsForTest(approvals []PendingApproval) []PendingApproval {
	out := make([]PendingApproval, len(approvals))
	copy(out, approvals)
	return out
}
