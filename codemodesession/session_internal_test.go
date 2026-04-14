package codemodesession

import (
	"context"
	"testing"
	"time"
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
