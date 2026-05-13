package client

import (
	"context"
	"errors"
	"testing"

	connect "connectrpc.com/connect"
	"github.com/solidarity-ai/toolbox/secrets"
)

func TestMapSecretStoreErrorNotInitialized(t *testing.T) {
	err := connect.NewError(connect.CodeFailedPrecondition, secrets.ErrNotInitialized)
	mapped := mapSecretStoreError(err)
	if !errors.Is(mapped, secrets.ErrNotInitialized) {
		t.Fatalf("mapSecretStoreError() = %v, want ErrNotInitialized", mapped)
	}
}

func TestMapSecretStoreErrorLocked(t *testing.T) {
	err := connect.NewError(connect.CodeFailedPrecondition, secrets.ErrLocked)
	mapped := mapSecretStoreError(err)
	if !errors.Is(mapped, secrets.ErrLocked) {
		t.Fatalf("mapSecretStoreError() = %v, want ErrLocked", mapped)
	}
}

func TestMapSecretStoreErrorContext(t *testing.T) {
	err := connect.NewError(connect.CodeCanceled, context.Canceled)
	mapped := mapSecretStoreError(err)
	if !errors.Is(mapped, context.Canceled) {
		t.Fatalf("mapSecretStoreError() = %v, want context.Canceled", mapped)
	}
}
