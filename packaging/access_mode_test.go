package packaging_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type inferAccessModeTestCase struct {
	name         string
	manifest     string
	wantMode     tooldef.AccessMode
	wantWarnings int
}

func TestLoadDevWithModeInfersAccessModeInDev(t *testing.T) {
	t.Parallel()

	for _, tt := range devAccessModeInferenceCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			manifestPath := filepath.Join(dir, packaging.DevManifestFilename)
			if err := os.WriteFile(manifestPath, []byte(tt.manifest), 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}

			result, err := packaging.LoadDevWithMode(dir, packaging.ValidationModeDev)
			if err != nil {
				t.Fatalf("load package dir: %v", err)
			}
			if len(result.Warnings) != tt.wantWarnings {
				t.Fatalf("expected %d warnings, got %d", tt.wantWarnings, len(result.Warnings))
			}
			if len(result.Loaded.Package.Tools) != 1 {
				t.Fatalf("expected 1 tool, got %d", len(result.Loaded.Package.Tools))
			}
			if got := result.Loaded.Package.Tools[0].AccessMode; got != tt.wantMode {
				t.Fatalf("expected inferred access mode %q, got %q", tt.wantMode, got)
			}
		})
	}
}

func devAccessModeInferenceCases() []inferAccessModeTestCase {
	cases := []struct {
		verb string
		mode tooldef.AccessMode
	}{
		{verb: "list", mode: tooldef.AccessModeReadOnly},
		{verb: "get", mode: tooldef.AccessModeReadOnly},
		{verb: "read", mode: tooldef.AccessModeReadOnly},
		{verb: "fetch", mode: tooldef.AccessModeReadOnly},
		{verb: "search", mode: tooldef.AccessModeReadOnly},
		{verb: "find", mode: tooldef.AccessModeReadOnly},
		{verb: "describe", mode: tooldef.AccessModeReadOnly},
		{verb: "create", mode: tooldef.AccessModeAppendOnly},
		{verb: "add", mode: tooldef.AccessModeAppendOnly},
		{verb: "send", mode: tooldef.AccessModeAppendOnly},
		{verb: "post", mode: tooldef.AccessModeAppendOnly},
		{verb: "clone", mode: tooldef.AccessModeAppendOnly},
		{verb: "new", mode: tooldef.AccessModeAppendOnly},
		{verb: "update", mode: tooldef.AccessModeCanDestruct},
		{verb: "delete", mode: tooldef.AccessModeCanDestruct},
		{verb: "remove", mode: tooldef.AccessModeCanDestruct},
		{verb: "set", mode: tooldef.AccessModeCanDestruct},
		{verb: "put", mode: tooldef.AccessModeCanDestruct},
		{verb: "patch", mode: tooldef.AccessModeCanDestruct},
		{verb: "replace", mode: tooldef.AccessModeCanDestruct},
		{verb: "edit", mode: tooldef.AccessModeCanDestruct},
	}

	out := make([]inferAccessModeTestCase, 0, len(cases)+1)
	for _, tc := range cases {
		out = append(out, inferredAccessModeCase(tc.verb, tc.mode))
	}
	out = append(out, inferredAccessModeCase("sync", tooldef.AccessModeCanDestruct))
	return out
}

func inferredAccessModeCase(verb string, mode tooldef.AccessMode) inferAccessModeTestCase {
	entryTS := fmt.Sprintf("tools/users.%s.ts", verb)
	return inferAccessModeTestCase{
		name: fmt.Sprintf("dev infers %s for %s", mode, verb),
		manifest: fmt.Sprintf(`{
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": %q, "idempotent": true }
  ]
}`, entryTS),
		wantMode:     mode,
		wantWarnings: 0,
	}
}
