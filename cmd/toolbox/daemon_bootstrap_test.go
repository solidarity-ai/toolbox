package main

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
	"github.com/solidarity-ai/toolbox/toolset"
)

func stubSessionDaemon(t *testing.T) *atomic.Int32 {
	t.Helper()

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

func (fakeSessionDaemon) SetSecretEpochHandler(func()) {}

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
	var stderr bytes.Buffer

	bindSecretEpochReload(delegate, &stderr, func() error {
		return io.EOF
	})
	delegate.fire()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stderr.Len() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := stderr.String(); got != "toolbox daemon reload error: EOF\n" {
		t.Fatalf("stderr = %q, want %q", got, "toolbox daemon reload error: EOF\n")
	}
}

type recordingSessionDaemon struct {
	handler atomic.Value
}

func (d *recordingSessionDaemon) SetPreparedTools(toolset.PreparedToolset) {}

func (d *recordingSessionDaemon) SetSecretEpochHandler(fn func()) {
	d.handler.Store(fn)
}

func (d *recordingSessionDaemon) Close() error { return nil }

func (d *recordingSessionDaemon) fire() {
	value := d.handler.Load()
	if value == nil {
		return
	}
	value.(func())()
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
