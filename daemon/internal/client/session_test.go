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

func TestHandleStateUpdateFiresInstalledApprovalHandlerForNewDecision(t *testing.T) {
	reg := &SessionRegistration{}

	var (
		mu        sync.Mutex
		decisions []ApprovalDecision
	)
	reg.SetApprovalHandler(func(decision ApprovalDecision) {
		mu.Lock()
		defer mu.Unlock()
		decisions = append(decisions, decision)
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
	if len(decisions) != 1 {
		t.Fatalf("decisions len = %d, want 1", len(decisions))
	}
	if decisions[0].Action != "reject" {
		t.Fatalf("decisions[0].Action = %q, want %q", decisions[0].Action, "reject")
	}
	if decisions[0].ToolCallID != "tc-1" {
		t.Fatalf("decisions[0].ToolCallID = %q, want %q", decisions[0].ToolCallID, "tc-1")
	}
	if decisions[0].Message != "blocked" {
		t.Fatalf("decisions[0].Message = %q, want %q", decisions[0].Message, "blocked")
	}
}
