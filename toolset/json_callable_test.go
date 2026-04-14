package toolset_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestPreparedToolJSONCallable(t *testing.T) {
	t.Parallel()

	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{{
		Name: "json.ok",
		Sig: tooltest.NewTSSig(t, `
export default function tool(input: {
  subject: string;
  labels?: string[];
  meta: { retries?: number | null };
}) {
  return input.subject.length;
}
`),
		TS: inlineToolDef(`
export default function tool(input: {
  subject: string;
  labels?: string[];
  meta: { retries?: number | null };
}) {
  return input.subject.length;
}
`),
	}})

	tool, ok := prepared.Tool("json.ok")
	if !ok {
		t.Fatal("expected prepared tool")
	}
	if !tool.JSONCallable() {
		t.Fatalf("JSONCallable = false, want true (%s)", tool.JSONCallWhyNot())
	}
	if got := tool.JSONCallWhyNot(); got != "" {
		t.Fatalf("JSONCallWhyNot = %q, want empty", got)
	}
}

func TestPreparedToolJSONCallableRejectsDateParam(t *testing.T) {
	t.Parallel()

	prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{{
		Name: "json.nope",
		Sig: tooltest.NewTSSig(t, `
export default function tool(when: Date, subject: string) {
  return subject;
}
`),
		TS: inlineToolDef(`
export default function tool(when: Date, subject: string) {
  return subject;
}
`),
	}})

	tool, ok := prepared.Tool("json.nope")
	if !ok {
		t.Fatal("expected prepared tool")
	}
	if tool.JSONCallable() {
		t.Fatal("JSONCallable = true, want false")
	}
	if got := tool.JSONCallWhyNot(); got == "" || got == "Date" {
		t.Fatalf("JSONCallWhyNot = %q, want param-specific explanation", got)
	}
}

func TestPreparedToolJSONCallableRejectsAnyUnknownAndUndefined(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		source   string
		wantPart string
	}{
		{
			name: "any",
			source: `
export default function tool(input: any) {
  return input;
}
`,
			wantPart: "uses any",
		},
		{
			name: "unknown",
			source: `
export default function tool(input: unknown) {
  return input;
}
`,
			wantPart: "uses unknown",
		},
		{
			name: "undefined",
			source: `
export default function tool(input: string | undefined) {
  return input;
}
`,
			wantPart: "uses undefined",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared := toolset.NewPreparedToolset([]assembler.LoadedTool{{
				Name: "json.nope",
				Sig:  tooltest.NewTSSig(t, tc.source),
				TS:   inlineToolDef(tc.source),
			}})

			tool, ok := prepared.Tool("json.nope")
			if !ok {
				t.Fatal("expected prepared tool")
			}
			if tool.JSONCallable() {
				t.Fatal("JSONCallable = true, want false")
			}
			if got := tool.JSONCallWhyNot(); !strings.Contains(got, tc.wantPart) {
				t.Fatalf("JSONCallWhyNot = %q, want substring %q", got, tc.wantPart)
			}
		})
	}
}

func TestPreparedToolJSONCallableAllowsOptionalScalarParamFromFixture(t *testing.T) {
	t.Parallel()

	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("shared-types"), toolset.Config{})

	tool, ok := prepared.Tool("tickets.list")
	if !ok {
		t.Fatal("expected prepared tool")
	}
	if !tool.JSONCallable() {
		t.Fatalf("JSONCallable = false, want true (%s)", tool.JSONCallWhyNot())
	}
}

func inlineToolDef(source string) *tooldef.TSToolDef {
	return &tooldef.TSToolDef{
		Entry: "tools/test.ts",
		Files: fstest.MapFS{
			"tools/test.ts": &fstest.MapFile{Data: []byte(source)},
		},
	}
}
