package main

import (
	"io"
	"sync/atomic"
	"testing"

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

func (fakeSessionDaemon) Close() error { return nil }
