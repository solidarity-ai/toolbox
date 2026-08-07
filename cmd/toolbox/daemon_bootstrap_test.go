package main

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/codemodesession"
	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/toolset"
)

func stubSessionDaemon(t *testing.T) *atomic.Int32 {
	t.Helper()
	useShortDaemonDir(t)

	var calls atomic.Int32
	prev := ensureSessionDaemon
	ensureSessionDaemon = func(string, string, io.Writer) (daemon.SessionDelegate, error) {
		calls.Add(1)
		return fakeSessionDaemon{}, nil
	}
	t.Cleanup(func() {
		ensureSessionDaemon = prev
	})
	return &calls
}

type fakeSessionDaemon struct{}

func (fakeSessionDaemon) SetPreparedTools(toolset.PreparedToolset) {}

func (fakeSessionDaemon) SetSessionBinding(string, bool)          {}
func (fakeSessionDaemon) SetSessionIntent(string, string, string) {}

func (fakeSessionDaemon) SetPendingApprovals([]daemon.PendingApprovalSnapshot) {}

func (fakeSessionDaemon) SetSecretEpochHandler(func()) {}

func (fakeSessionDaemon) SetApprovalBatchHandler(func([]daemon.ApprovalDecision)) {}

func (fakeSessionDaemon) Close() error { return nil }

func TestBindSecretEpochReload(t *testing.T) {
	delegate := &recordingSessionDaemon{}
	var reloads atomic.Int32

	bindSecretEpochReload(delegate, io.Discard, func() error {
		reloads.Add(1)
		return nil
	})
	delegate.fire()

	waitForAtomic(t, &reloads, 1)
}

func TestBindSecretEpochReloadLogsFailures(t *testing.T) {
	delegate := &recordingSessionDaemon{}
	var stderr lockedBuffer

	bindSecretEpochReload(delegate, &stderr, func() error {
		return io.EOF
	})
	delegate.fire()

	if got := waitForBufferSubstring(t, &stderr, "toolbox daemon reload error: EOF"); got != "toolbox daemon reload error: EOF\n" {
		t.Fatalf("stderr = %q, want %q", got, "toolbox daemon reload error: EOF\n")
	}
}

func TestBindApprovalExecutionAppliesDecisionBatchTogether(t *testing.T) {
	delegate := &recordingSessionDaemon{}
	executor := &recordingApprovalExecutor{calls: make(chan []codemodesession.ApprovalDecision, 1)}

	bindApprovalExecution(delegate, io.Discard, executor, nil)
	delegate.fireApprovalBatch([]daemon.ApprovalDecision{{
		Action:     daemon.ApprovalActionReject,
		ToolCallID: "tc-1",
		Message:    "blocked",
	}, {
		Action:     daemon.ApprovalActionReject,
		ToolCallID: "tc-2",
		Message:    "blocked",
	}})

	select {
	case got := <-executor.calls:
		if len(got) != 2 {
			t.Fatalf("ApplyApprovals batch len = %d, want 2: %#v", len(got), got)
		}
		if got[0].ToolCallID != "tc-1" || got[1].ToolCallID != "tc-2" {
			t.Fatalf("ApplyApprovals tool call ids = %#v, want tc-1/tc-2", got)
		}
		if got[0].Approved || got[1].Approved {
			t.Fatalf("ApplyApprovals approved flags = %#v, want both false", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyApprovals was not called")
	}
}

func TestBindApprovalExecutionRecoversAndLogsPanic(t *testing.T) {
	delegate := &recordingSessionDaemon{}
	executor := panicApprovalExecutor{}
	var stderr lockedBuffer

	bindApprovalExecution(delegate, &stderr, executor, nil)
	delegate.fireApprovalBatch([]daemon.ApprovalDecision{{
		Action:     daemon.ApprovalActionReject,
		ToolCallID: "tc-1",
		Message:    "blocked",
	}, {
		Action:     daemon.ApprovalActionReject,
		ToolCallID: "tc-2",
		Message:    "blocked",
	}})

	got := waitForBufferSubstring(t, &stderr, "toolbox daemon approval panic applying 2 decision(s): boom")
	if got == "" {
		t.Fatal("stderr was empty, want recovered approval panic")
	}
	if !containsAll(got, "toolbox daemon approval panic applying 2 decision(s): boom", "goroutine") {
		t.Fatalf("stderr = %q, want panic message and stack", got)
	}
}

type recordingApprovalExecutor struct {
	calls chan []codemodesession.ApprovalDecision
}

func (e *recordingApprovalExecutor) ApplyApprovals(_ context.Context, decisions []codemodesession.ApprovalDecision) error {
	e.calls <- append([]codemodesession.ApprovalDecision(nil), decisions...)
	return nil
}

type panicApprovalExecutor struct{}

func (panicApprovalExecutor) ApplyApprovals(context.Context, []codemodesession.ApprovalDecision) error {
	panic("boom")
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

type recordingSessionDaemon struct {
	handler        atomic.Value
	approvalBatch  atomic.Value
	boundTBSession string
	locked         bool
}

func (d *recordingSessionDaemon) SetPreparedTools(toolset.PreparedToolset) {}

func (d *recordingSessionDaemon) SetSessionBinding(boundTBSession string, locked bool) {
	d.boundTBSession = boundTBSession
	d.locked = locked
}
func (d *recordingSessionDaemon) SetSessionIntent(string, string, string) {}

func (d *recordingSessionDaemon) SetPendingApprovals([]daemon.PendingApprovalSnapshot) {}

func (d *recordingSessionDaemon) SetSecretEpochHandler(fn func()) {
	d.handler.Store(fn)
}

func (d *recordingSessionDaemon) SetApprovalBatchHandler(fn func([]daemon.ApprovalDecision)) {
	d.approvalBatch.Store(fn)
}

func (d *recordingSessionDaemon) Close() error { return nil }

func (d *recordingSessionDaemon) fire() {
	value := d.handler.Load()
	if value == nil {
		return
	}
	value.(func())()
}

func (d *recordingSessionDaemon) fireApprovalBatch(decisions []daemon.ApprovalDecision) {
	value := d.approvalBatch.Load()
	if value == nil {
		return
	}
	value.(func([]daemon.ApprovalDecision))(decisions)
}

func waitForAtomic(t *testing.T, value *atomic.Int32, want int32) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if value.Load() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("atomic value = %d, want %d", value.Load(), want)
}

type staticSessionBinding struct {
	boundTBSession string
	locked         bool
}

func (s staticSessionBinding) BoundTBSession() string { return s.boundTBSession }

func (s staticSessionBinding) Locked() bool { return s.locked }

func TestSyncSessionBindingCopiesManagedState(t *testing.T) {
	delegate := &recordingSessionDaemon{}

	syncSessionBinding(delegate, staticSessionBinding{boundTBSession: "abc123", locked: true})
	if delegate.boundTBSession != "abc123" || !delegate.locked {
		t.Fatalf("delegate binding = (%q, %t), want (%q, %t)", delegate.boundTBSession, delegate.locked, "abc123", true)
	}

	syncSessionBinding(delegate, staticSessionBinding{boundTBSession: "def456", locked: true})
	if delegate.boundTBSession != "def456" || !delegate.locked {
		t.Fatalf("delegate binding after refresh = (%q, %t), want (%q, %t)", delegate.boundTBSession, delegate.locked, "def456", true)
	}

	syncSessionBinding(delegate, staticSessionBinding{})
	if delegate.boundTBSession != "" || delegate.locked {
		t.Fatalf("delegate unlocked binding = (%q, %t), want (%q, %t)", delegate.boundTBSession, delegate.locked, "", false)
	}
}
