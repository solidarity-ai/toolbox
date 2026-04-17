package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForBufferSubstring(t testing.TB, buf *lockedBuffer, needle string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := buf.String()
		if strings.Contains(got, needle) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return buf.String()
}
