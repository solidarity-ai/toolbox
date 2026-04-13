package tooltest

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/microsoft/typescript-go/toolbox"
)

// NewTSSig extracts a function signature from a small in-memory TypeScript module.
// The source should contain a default-exported function.
func NewTSSig(t testing.TB, source string) *toolbox.FuncSignature {
	t.Helper()
	meta, err := toolbox.ExtractToolMetadata(context.Background(), toolbox.ExtractInput{
		Files: fstest.MapFS{
			"tools/test.ts": &fstest.MapFile{Data: []byte(source)},
		},
		Entry: "tools/test.ts",
	})
	if err != nil {
		t.Fatalf("extract TS signature: %v", err)
	}
	if meta.Sig == nil {
		t.Fatal("expected non-nil TS signature")
	}
	return meta.Sig
}
