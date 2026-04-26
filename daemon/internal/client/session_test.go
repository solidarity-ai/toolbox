//go:build !windows

package client

import (
	"sync"
	"sync/atomic"
	"testing"

	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
)

func TestSetSecretEpochHandlerDoesNotFireForInitialBaseline(t *testing.T) {
	var changes atomic.Int32
	reg := &SessionRegistration{}

	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-1"})
	reg.SetSecretEpochHandler(func() {
		changes.Add(1)
	})

	if got := changes.Load(); got != 0 {
		t.Fatalf("changes = %d, want 0", got)
	}
}

func TestSetSecretEpochHandlerFiresForPendingEpochChange(t *testing.T) {
	var changes atomic.Int32
	reg := &SessionRegistration{}

	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-1"})
	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-2"})
	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-3"})

	reg.SetSecretEpochHandler(func() {
		changes.Add(1)
	})

	if got := changes.Load(); got != 1 {
		t.Fatalf("changes = %d, want 1", got)
	}
	if reg.pendingSecretEpochChange {
		t.Fatal("pendingSecretEpochChange = true, want false after handler install")
	}
}

func TestHandleStateUpdateFiresInstalledHandlerForNewEpochChange(t *testing.T) {
	var changes atomic.Int32
	reg := &SessionRegistration{}

	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-1"})
	reg.SetSecretEpochHandler(func() {
		changes.Add(1)
	})
	reg.handleStateUpdate(&daemonv1.StateUpdate{SecretEpoch: "epoch-2"})

	if got := changes.Load(); got != 1 {
		t.Fatalf("changes = %d, want 1", got)
	}
}

func TestHandleStateUpdateFiresInstalledApprovalBatchHandlerForNewDecision(t *testing.T) {
	reg := &SessionRegistration{}

	var (
		mu      sync.Mutex
		batches [][]ApprovalDecision
	)
	reg.SetApprovalBatchHandler(func(decisions []ApprovalDecision) {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, append([]ApprovalDecision(nil), decisions...))
	})

	reg.handleStateUpdate(&daemonv1.StateUpdate{
		ApprovalDecisions: []*daemonv1.ApprovalDecision{{
			Action:     "reject",
			ToolCallId: "tc-1",
			Message:    "blocked",
		}},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("batches len = %d, want 1", len(batches))
	}
	if len(batches[0]) != 1 {
		t.Fatalf("batches[0] len = %d, want 1", len(batches[0]))
	}
	if batches[0][0].Action != "reject" {
		t.Fatalf("batches[0][0].Action = %q, want %q", batches[0][0].Action, "reject")
	}
	if batches[0][0].ToolCallID != "tc-1" {
		t.Fatalf("batches[0][0].ToolCallID = %q, want %q", batches[0][0].ToolCallID, "tc-1")
	}
	if batches[0][0].Message != "blocked" {
		t.Fatalf("batches[0][0].Message = %q, want %q", batches[0][0].Message, "blocked")
	}
}

func TestHandleStateUpdatePreservesApprovalDecisionBatch(t *testing.T) {
	reg := &SessionRegistration{}

	var (
		mu      sync.Mutex
		batches [][]ApprovalDecision
	)
	reg.SetApprovalBatchHandler(func(decisions []ApprovalDecision) {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, append([]ApprovalDecision(nil), decisions...))
	})

	reg.handleStateUpdate(&daemonv1.StateUpdate{
		ApprovalDecisions: []*daemonv1.ApprovalDecision{{
			Action:     "reject",
			ToolCallId: "tc-1",
			Message:    "first",
		}, {
			Action:     "reject",
			ToolCallId: "tc-2",
			Message:    "second",
		}},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("batches len = %d, want 1", len(batches))
	}
	if len(batches[0]) != 2 {
		t.Fatalf("batches[0] len = %d, want 2", len(batches[0]))
	}
	if batches[0][0].ToolCallID != "tc-1" || batches[0][1].ToolCallID != "tc-2" {
		t.Fatalf("batch tool call ids = %#v, want tc-1/tc-2", batches[0])
	}
}
