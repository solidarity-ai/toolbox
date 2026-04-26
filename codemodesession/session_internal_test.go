package codemodesession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	repl "github.com/mackross/repljs"
	storemem "github.com/mackross/repljs/store/mem"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
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

func TestToolRunStateAcquireCancelsWithParentContext(t *testing.T) {
	state := newToolRunState()
	parent, cancel := context.WithCancel(context.Background())
	ctx, release := state.Acquire(parent)
	defer release()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("acquired tool context did not cancel with parent context")
	}
}

func TestApprovalAwaitBatchTextListsEveryRejectedDecision(t *testing.T) {
	result := approvalAwaitResultFromBatch([]appliedApprovalResult{{
		Status: ApprovalAwaitStatusRejected,
		ToolCall: PendingApproval{
			ToolCallID: "tc-1",
			ToolName:   "gmail.send",
			Error:      "wrong recipient",
		},
	}, {
		Status: ApprovalAwaitStatusRejected,
		ToolCall: PendingApproval{
			ToolCallID: "tc-2",
			ToolName:   "calendar.create",
			Error:      "wrong time",
		},
	}}, 0)

	text := result.Text()
	for _, want := range []string{
		"2 approval decisions applied (0 approved, 2 rejected).",
		"- gmail.send [tc-1] rejected: wrong recipient",
		"- calendar.create [tc-2] rejected: wrong time",
		"(no outstanding approvals).",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("result text missing %q:\n%s", want, text)
		}
	}
}

func TestToolRunStateResetCancelsRunningToolsWithoutWaiting(t *testing.T) {
	state := newToolRunState()
	ctx, release := state.Acquire(context.Background())
	defer release()

	resetDone := make(chan struct{})
	go func() {
		state.Reset()
		close(resetDone)
	}()

	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("Reset() blocked on an in-flight tool run")
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Reset() did not cancel the prior generation context")
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

func TestSubmitFailsBeforeInlineToolStartsWhenToolCallStartJournalFails(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{}, 1)
	prepared, err := toolset.PrepareTools(ctx, []assembler.LoadedTool{{
		Name: "demo.run",
		PackageMeta: &tooldef.Package{
			Module:  "example.com/demo",
			Name:    "demo",
			Runtime: tooldef.RuntimeBuiltin,
		},
		BuiltIn: func(context.Context, map[string]any) (string, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			return "ok", nil
		},
	}}, toolset.Config{})
	if err != nil {
		t.Fatalf("PrepareTools(demo): %v", err)
	}

	st := storemem.New()
	approvals := newMemoryApprovalStore()
	toolCalls := &failingStartToolCallJournal{
		wrapped: newMemoryToolCallJournal(st),
		err:     errors.New("boom"),
	}
	toolRuns := newToolRunState()
	executor := invoke.NewExecutor(prepared)
	sess, err := repl.New().StartSession(ctx, repl.SessionConfig{
		Manifest: repl.Manifest{ID: manifestID},
	}, sessionDeps(t.TempDir(), st, func() toolset.PreparedToolset { return prepared }, toolCalls, approvals, executor, toolRuns.Acquire))
	if err != nil {
		t.Fatalf("StartSession(): %v", err)
	}

	session := &Session{
		session:        sess,
		store:          st,
		id:             sess.ID(),
		prepared:       newPreparedState(prepared),
		applied:        prepared,
		toolCalls:      toolCalls,
		approvals:      approvals,
		approvalAwaits: newApprovalAwaitDelegate(),
		toolRuns:       toolRuns,
		executor:       executor,
		ownExecutor:    true,
	}
	defer session.Close()

	out := session.Submit(ctx, `await demo.demo.run()`)
	if !strings.Contains(out, "start journal: boom") {
		t.Fatalf("Submit() = %q, want start journal failure", out)
	}

	select {
	case <-started:
		t.Fatal("tool started executing before durable tool-call start succeeded")
	case <-time.After(150 * time.Millisecond):
	}
}

type blockingApprovalStore struct {
	approvals []PendingApproval

	pendingEntered chan struct{}
	releasePending chan struct{}
}

type failingStartToolCallJournal struct {
	wrapped toolCallJournal
	err     error
}

func (j *failingStartToolCallJournal) EnsureStarted(repl.SessionID, string, string, []byte) error {
	return j.err
}

func (j *failingStartToolCallJournal) EnsureNeedsApproval(sessionID repl.SessionID, toolCallID, approvalID, toolName string, params []byte) error {
	return j.wrapped.EnsureNeedsApproval(sessionID, toolCallID, approvalID, toolName, params)
}

func (j *failingStartToolCallJournal) EnsureCompleted(sessionID repl.SessionID, toolCallID string, result []byte) error {
	return j.wrapped.EnsureCompleted(sessionID, toolCallID, result)
}

func (j *failingStartToolCallJournal) EnsureFailed(sessionID repl.SessionID, toolCallID, errText string) error {
	return j.wrapped.EnsureFailed(sessionID, toolCallID, errText)
}

func (j *failingStartToolCallJournal) EnsureCancelled(sessionID repl.SessionID, toolCallID string) error {
	return j.wrapped.EnsureCancelled(sessionID, toolCallID)
}

func (j *failingStartToolCallJournal) EnsureUnknown(sessionID repl.SessionID, toolCallID string) error {
	return j.wrapped.EnsureUnknown(sessionID, toolCallID)
}

func (j *failingStartToolCallJournal) Snapshot(sessionID repl.SessionID, toolCallID string) (toolCallSnapshot, bool, error) {
	return j.wrapped.Snapshot(sessionID, toolCallID)
}

func (j *failingStartToolCallJournal) Recover(ctx context.Context, sessionID repl.SessionID) error {
	return j.wrapped.Recover(ctx, sessionID)
}

func (s *blockingApprovalStore) BeginSubmit(repl.SessionID) {}

func (s *blockingApprovalStore) AbortSubmit(repl.SessionID) {}

func (s *blockingApprovalStore) RecordPendingToolCall(repl.SessionID, approvalCallState) error {
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

func (s *blockingApprovalStore) ApplyDecisions(context.Context, repl.SessionID, []ApprovalDecision, toolset.PreparedToolset, repl.Store, toolCallJournal, *invoke.Executor) ([]appliedApprovalResult, error) {
	return nil, errors.New("unexpected call")
}

func (s *blockingApprovalStore) RecoverSession(context.Context, repl.SessionID, toolCallJournal) error {
	return nil
}

func clonePendingApprovalsForTest(approvals []PendingApproval) []PendingApproval {
	out := make([]PendingApproval, len(approvals))
	copy(out, approvals)
	return out
}
