package toolsetctl

import (
	"testing"

	"github.com/mackross/repljs/jswire"
)

func TestDecodeBuiltInArgs_UnwrapsPlainParamsObject(t *testing.T) {
	var req InstallRequest
	args := map[string]any{"params": map[string]any{"package": "github.com/include-tools/hacker-news"}}
	if err := decodeBuiltInArgs(args, &req); err != nil {
		t.Fatalf("decodeBuiltInArgs: %v", err)
	}
	if req.Package != "github.com/include-tools/hacker-news" {
		t.Fatalf("req = %+v, want package decoded", req)
	}
}

func TestDecodeBuiltInArgs_UnwrapsTypedWireParamsObject(t *testing.T) {
	var req InstallRequest
	args := map[string]any{"params": jswire.ObjectType{"package": "github.com/include-tools/hacker-news"}}
	if err := decodeBuiltInArgs(args, &req); err != nil {
		t.Fatalf("decodeBuiltInArgs: %v", err)
	}
	if req.Package != "github.com/include-tools/hacker-news" {
		t.Fatalf("req = %+v, want package decoded", req)
	}
}
