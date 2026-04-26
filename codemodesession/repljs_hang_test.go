//go:build repljs_hang

package codemodesession

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplJSLargeArrayCompletionDoesNotHang(t *testing.T) {
	// Reproduces a live reopen hang from a historical cell that evaluated
	// Object.keys($pkgMetadata["google-workspace"].api), where api was a long
	// string and the completion value became a large dense array.
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	session, err := CreateNew(ctx, "abc123", t.TempDir())
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}

	out := submitWithTimeout(t, session, ctx, `Object.keys("x".repeat(13000))`, 2*time.Second)
	if !strings.Contains(out, "cell 1") {
		t.Fatalf("Submit(large Object.keys completion) = %q, want committed cell", out)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func submitWithTimeout(t *testing.T, session *Session, ctx context.Context, source string, timeout time.Duration) string {
	t.Helper()

	done := make(chan string, 1)
	go func() {
		done <- session.Submit(ctx, source)
	}()

	select {
	case out := <-done:
		return out
	case <-time.After(timeout):
		t.Fatalf("Submit(%q) did not return within %s; likely stuck encoding the completion value", source, timeout)
		return ""
	}
}
