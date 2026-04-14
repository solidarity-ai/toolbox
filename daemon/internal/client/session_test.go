//go:build !windows

package client

import (
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
