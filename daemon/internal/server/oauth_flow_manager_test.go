package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestOAuthFlowManagerExpiresFlowAndRejectsLateCallback(t *testing.T) {
	manager := newOAuthFlowManager(5 * time.Minute)
	manager.now = func() time.Time { return time.Unix(100, 0).UTC() }
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-expire", "https://provider.example/auth", "Authorize test")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	waitObserved := observeOAuthWaitLookup(manager, manager.now)
	waitErr := make(chan error, 1)
	go func() {
		_, err := manager.Wait(context.Background(), flow.flowID)
		waitErr <- err
	}()
	waitForOAuthWaitLookup(t, waitObserved)

	manual.fire()

	err = <-waitErr
	if !isOAuthFlowErrorKind(err, oauthFlowErrorExpired) {
		t.Fatalf("Wait() error = %v, want expired flow error", err)
	}
	if err := manager.Complete("state-expire", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(expired) error = %v, want not found", err)
	}
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(expired again) error = %v, want not found", err)
	}
}

func TestOAuthFlowManagerUsesDefaultFiveMinuteTTL(t *testing.T) {
	manager := newOAuthFlowManager(0)
	now := time.Unix(200, 0).UTC()
	manager.now = func() time.Time { return now }
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-default-ttl", "https://provider.example/auth", "")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}
	if got, want := flow.expiresAt.Sub(now), 5*time.Minute; got != want {
		t.Fatalf("expiresAt-now = %v, want %v", got, want)
	}
	if got, want := manual.duration(), 5*time.Minute; got != want {
		t.Fatalf("timer duration = %v, want %v", got, want)
	}
}

func TestOAuthFlowManagerPendingExpiresDueFlows(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	manager := newOAuthFlowManager(5 * time.Minute)
	manager.now = func() time.Time { return now }
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-pending-expire", "https://provider.example/auth", "Authorize stale")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	now = now.Add(5*time.Minute + time.Second)
	if got := manager.Pending(); len(got) != 0 {
		t.Fatalf("Pending() after expiry = %#v, want none", got)
	}
	if !manual.stoppedState() {
		t.Fatal("Pending() expiry did not stop flow timer")
	}
	if err := manager.Complete("state-pending-expire", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(expired-by-pending) error = %v, want not found", err)
	}
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(expired-by-pending) error = %v, want not found", err)
	}
}

func TestOAuthFlowManagerCompleteExpiresDueFlowBeforeTimerFires(t *testing.T) {
	now := time.Unix(350, 0).UTC()
	manager := newOAuthFlowManager(5 * time.Minute)
	manager.now = func() time.Time { return now }
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-complete-after-deadline", "https://provider.example/auth", "Authorize stale")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	now = now.Add(5*time.Minute + time.Second)
	if err := manager.Complete("state-complete-after-deadline", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(due-expired) error = %v, want not found", err)
	}
	if !manual.stoppedState() {
		t.Fatal("Complete() expiry did not stop flow timer")
	}
	if got := manager.Pending(); len(got) != 0 {
		t.Fatalf("Pending() after Complete-triggered expiry = %#v, want none", got)
	}
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(after Complete-triggered expiry) error = %v, want not found", err)
	}
}

func TestOAuthFlowManagerWaitExpiresDueFlowBeforeTimerFires(t *testing.T) {
	now := time.Unix(375, 0).UTC()
	manager := newOAuthFlowManager(5 * time.Minute)
	manager.now = func() time.Time { return now }
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-wait-after-deadline", "https://provider.example/auth", "Authorize stale")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	now = now.Add(5*time.Minute + time.Second)
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorExpired) {
		t.Fatalf("Wait(due-expired) error = %v, want expired", err)
	}
	if !manual.stoppedState() {
		t.Fatal("Wait() expiry did not stop flow timer")
	}
	if err := manager.Complete("state-wait-after-deadline", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(after Wait-triggered expiry) error = %v, want not found", err)
	}
}

func TestOAuthFlowManagerCompleteBeforeWaitKeepsResultAndWaitStopsTimer(t *testing.T) {
	manager := newOAuthFlowManager(5 * time.Minute)
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-complete-before-wait", "https://provider.example/auth", "Authorize complete")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	if err := manager.Complete("state-complete-before-wait", OAuthCallbackResult{Code: "code-before-wait"}); err != nil {
		t.Fatalf("Complete(): %v", err)
	}

	result, err := manager.Wait(context.Background(), flow.flowID)
	if err != nil {
		t.Fatalf("Wait(): %v", err)
	}
	if got, want := result.Code, "code-before-wait"; got != want {
		t.Fatalf("Wait() code = %q, want %q", got, want)
	}
	if got, want := result.State, "state-complete-before-wait"; got != want {
		t.Fatalf("Wait() state = %q, want %q", got, want)
	}
	if !manual.stoppedState() {
		t.Fatal("Wait() did not stop completed flow timer")
	}
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(consumed) error = %v, want not found", err)
	}
	if err := manager.Complete("state-complete-before-wait", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(consumed) error = %v, want not found", err)
	}
}

func TestOAuthFlowManagerCompletedFlowExpiresIfNeverWaited(t *testing.T) {
	manager := newOAuthFlowManager(5 * time.Minute)
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-complete-no-wait", "https://provider.example/auth", "Authorize complete")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}
	if err := manager.Complete("state-complete-no-wait", OAuthCallbackResult{Code: "code-no-wait"}); err != nil {
		t.Fatalf("Complete(): %v", err)
	}
	if manual.stoppedState() {
		t.Fatal("Complete() stopped cleanup timer before Wait consumed the result")
	}

	manual.fire()

	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(cleaned-up completed flow) error = %v, want not found", err)
	}
	if got := manager.Pending(); len(got) != 0 {
		t.Fatalf("Pending() after completed flow cleanup = %#v, want none", got)
	}
}

func TestOAuthFlowManagerCancelStopsTimerAndRemovesFlow(t *testing.T) {
	manager := newOAuthFlowManager(5 * time.Minute)
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-cancel", "https://provider.example/auth", "Authorize cancel")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	if err := manager.Cancel(flow.flowID); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}
	if !manual.stoppedState() {
		t.Fatal("Cancel() did not stop flow timer")
	}
	if got := manager.Pending(); len(got) != 0 {
		t.Fatalf("Pending() after Cancel = %#v, want none", got)
	}
	if _, err := manager.Wait(context.Background(), flow.flowID); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Wait(canceled) error = %v, want not found", err)
	}
	if err := manager.Complete("state-cancel", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(canceled) error = %v, want not found", err)
	}
	if err := manager.Cancel(flow.flowID); err != nil {
		t.Fatalf("Cancel(idempotent): %v", err)
	}
}

func TestOAuthFlowManagerCloseCancelsPendingWaitersAndStopsTimers(t *testing.T) {
	manager := newOAuthFlowManager(5 * time.Minute)
	manual := newManualOAuthTimer()
	manager.afterFunc = manual.afterFunc

	flow, err := manager.Begin("state-close", "https://provider.example/auth", "Authorize close")
	if err != nil {
		t.Fatalf("Begin(): %v", err)
	}

	waitObserved := observeOAuthWaitLookup(manager, manager.now)
	waitErr := make(chan error, 1)
	go func() {
		_, err := manager.Wait(context.Background(), flow.flowID)
		waitErr <- err
	}()
	waitForOAuthWaitLookup(t, waitObserved)

	manager.Close()

	err = <-waitErr
	if !isOAuthFlowErrorKind(err, oauthFlowErrorCanceled) {
		t.Fatalf("Wait() after Close error = %v, want canceled flow error", err)
	}
	if !manual.stoppedState() {
		t.Fatal("Close() did not stop flow timer")
	}
	if got := manager.Pending(); len(got) != 0 {
		t.Fatalf("Pending() after Close = %#v, want none", got)
	}
	if err := manager.Complete("state-close", OAuthCallbackResult{Code: "late-code"}); !isOAuthFlowErrorKind(err, oauthFlowErrorNotFound) {
		t.Fatalf("Complete(closed) error = %v, want not found", err)
	}
}

func isOAuthFlowErrorKind(err error, kind oauthFlowErrorKind) bool {
	flowErr, ok := err.(*oauthFlowError)
	return ok && flowErr.kind == kind
}

func observeOAuthWaitLookup(manager *oauthFlowManager, now func() time.Time) <-chan struct{} {
	started := make(chan struct{}, 8)
	manager.now = func() time.Time {
		select {
		case started <- struct{}{}:
		default:
		}
		return now()
	}
	return started
}

func waitForOAuthWaitLookup(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OAuth waiter to observe pending flow")
	}
}

type manualOAuthTimer struct {
	mu      sync.Mutex
	d       time.Duration
	fn      func()
	stopped bool
}

func newManualOAuthTimer() *manualOAuthTimer {
	return &manualOAuthTimer{}
}

func (t *manualOAuthTimer) afterFunc(d time.Duration, fn func()) oauthFlowTimer {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.d = d
	t.fn = fn
	return t
}

func (t *manualOAuthTimer) duration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.d
}

func (t *manualOAuthTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

func (t *manualOAuthTimer) stoppedState() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

func (t *manualOAuthTimer) fire() {
	t.mu.Lock()
	fn := t.fn
	stopped := t.stopped
	if !stopped {
		t.stopped = true
	}
	t.mu.Unlock()
	if fn != nil && !stopped {
		fn()
	}
}
