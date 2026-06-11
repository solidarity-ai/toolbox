package toolpkgdiscovery

import (
	"testing"

	"github.com/mackross/repljs/jswire"
)

func TestDecodeBuiltInArgs_UnwrapsPlainParamsObject(t *testing.T) {
	var req SearchRequest
	args := map[string]any{"params": map[string]any{"query": "hacker news", "limit": float64(5)}}
	if err := decodeBuiltInArgs(args, &req); err != nil {
		t.Fatalf("decodeBuiltInArgs: %v", err)
	}
	if req.Query != "hacker news" || req.Limit != 5 {
		t.Fatalf("req = %+v, want query and limit decoded", req)
	}
}

func TestDecodeBuiltInArgs_UnwrapsTypedWireParamsObject(t *testing.T) {
	var req SearchRequest
	args := map[string]any{"params": jswire.ObjectType{"query": "hacker news", "limit": float64(5)}}
	if err := decodeBuiltInArgs(args, &req); err != nil {
		t.Fatalf("decodeBuiltInArgs: %v", err)
	}
	if req.Query != "hacker news" || req.Limit != 5 {
		t.Fatalf("req = %+v, want query and limit decoded", req)
	}
}
