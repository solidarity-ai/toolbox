package packaging_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type inferEffectTestCase struct {
	name         string
	manifest     string
	entryTS      string
	wantEffect   tooldef.Effect
	wantWarnings int
}

func TestLoadDevWithModeInfersEffectInDev(t *testing.T) {
	t.Parallel()

	for _, tt := range devEffectInferenceCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			manifestPath := filepath.Join(dir, packaging.DevManifestFilename)
			if err := os.WriteFile(manifestPath, []byte(tt.manifest), 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, tt.entryTS)), 0o755); err != nil {
				t.Fatalf("mkdir tool dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, tt.entryTS), []byte(`export default function tool() { return "ok"; }`), 0o644); err != nil {
				t.Fatalf("write tool source: %v", err)
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
			if got := result.Loaded.Package.Tools[0].Effect; got != tt.wantEffect {
				t.Fatalf("expected inferred effect %q, got %q", tt.wantEffect, got)
			}
		})
	}
}

func devEffectInferenceCases() []inferEffectTestCase {
	cases := []struct {
		verb string
		mode tooldef.Effect
	}{
		{verb: "list", mode: tooldef.EffectReadOnly},
		{verb: "get", mode: tooldef.EffectReadOnly},
		{verb: "read", mode: tooldef.EffectReadOnly},
		{verb: "fetch", mode: tooldef.EffectReadOnly},
		{verb: "search", mode: tooldef.EffectReadOnly},
		{verb: "find", mode: tooldef.EffectReadOnly},
		{verb: "describe", mode: tooldef.EffectReadOnly},
		{verb: "create", mode: tooldef.EffectReversible},
		{verb: "add", mode: tooldef.EffectReversible},
		{verb: "send", mode: tooldef.EffectIrreversible},
		{verb: "post", mode: tooldef.EffectIrreversible},
		{verb: "clone", mode: tooldef.EffectReversible},
		{verb: "new", mode: tooldef.EffectReversible},
		{verb: "update", mode: tooldef.EffectIrreversible},
		{verb: "delete", mode: tooldef.EffectIrreversible},
		{verb: "remove", mode: tooldef.EffectIrreversible},
		{verb: "set", mode: tooldef.EffectIrreversible},
		{verb: "put", mode: tooldef.EffectIrreversible},
		{verb: "patch", mode: tooldef.EffectIrreversible},
		{verb: "replace", mode: tooldef.EffectIrreversible},
		{verb: "edit", mode: tooldef.EffectIrreversible},
	}

	out := make([]inferEffectTestCase, 0, len(cases)+1)
	for _, tc := range cases {
		out = append(out, inferredEffectCase(tc.verb, tc.mode))
	}
	out = append(out, inferredEffectCase("sync", tooldef.EffectIrreversible))
	return out
}

func inferredEffectCase(verb string, mode tooldef.Effect) inferEffectTestCase {
	entryTS := fmt.Sprintf("tools/users.%s.ts", verb)
	return inferEffectTestCase{
		name:    fmt.Sprintf("dev infers %s for %s", mode, verb),
		entryTS: entryTS,
		manifest: fmt.Sprintf(`{
  "module": "example.com/calc",
  "name": "calc",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": %q, "idempotent": true }
  ]
}`, entryTS),
		wantEffect:   mode,
		wantWarnings: 0,
	}
}
