package codemodesdks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/codemodesdks"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestDeclarationAndSchemaGoldens(t *testing.T) {
	variants := []struct {
		name string
		cfg  toolset.Config
	}{
		{
			name: "passthrough",
			cfg:  toolset.Config{},
		},
		{
			name: "bound",
			cfg: toolset.Config{
				EnvContext: map[string]any{"fixed_a": 42},
				Tools: []toolset.BoundTool{
					{ToolRef: "calc.add", Bindings: map[string]toolset.Binding{
						"a": {Value: "context.fixed_a"},
					}},
				},
			},
		},
		{
			name: "hidden",
			cfg: toolset.Config{
				EnvContext: map[string]any{"fixed_a": 42},
				Tools: []toolset.BoundTool{
					{ToolRef: "calc.add", Bindings: map[string]toolset.Binding{
						"a": {Value: "context.fixed_a", Hidden: true},
					}},
				},
			},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), v.cfg)

			prefix := "calc-" + v.name
			checkGolden(t, goldenPath(prefix+".d.ts"), codemodesdks.DeclarationSource(prepared))
			checkGolden(t, goldenPath(prefix+".schema.json"), schemaGoldenSource(t, prepared))
		})
	}
}

func TestEdgeCaseGoldens(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("edge-cases"), toolset.Config{})

	checkGolden(t, goldenPath("edge-cases-passthrough.d.ts"), codemodesdks.DeclarationSource(prepared))
	checkGolden(t, goldenPath("edge-cases-passthrough.schema.json"), schemaGoldenSource(t, prepared))
}

func TestSharedTypesGoldens(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("shared-types"), toolset.Config{})

	checkGolden(t, goldenPath("shared-types-passthrough.d.ts"), codemodesdks.DeclarationSource(prepared))
	checkGolden(t, goldenPath("shared-types-passthrough.schema.json"), schemaGoldenSource(t, prepared))
}

func TestGithubIssuesGoldens(t *testing.T) {
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("github-issues"), toolset.Config{})

	checkGolden(t, goldenPath("github-issues-passthrough.d.ts"), codemodesdks.DeclarationSource(prepared))
	checkGolden(t, goldenPath("github-issues-passthrough.schema.json"), schemaGoldenSource(t, prepared))
}

func goldenPath(name string) string {
	return filepath.Join(testfileDir(), "..", "testutil", "testdata", "goldens", "codemode", name)
}

func schemaGoldenSource(t *testing.T, prepared toolset.PreparedToolset) string {
	t.Helper()
	view := prepared.AgentView()
	schemaMap := buildSchemaMap(view)
	schemaJSON, err := json.MarshalIndent(schemaMap, "", "  ")
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return string(schemaJSON) + "\n"
}

func checkGolden(t *testing.T, path string, got string) {
	t.Helper()
	if os.Getenv("GENERATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with GENERATE_GOLDENS=1 to create): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden mismatch (-want +got):\n%s", diff)
	}
}

func buildSchemaMap(view toolset.AgentView) map[string]any {
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	out := make(map[string]any, len(tools))
	for _, tool := range tools {
		if pt := tool.ParamsType(); pt != nil {
			out[tool.Name] = pt.ToJSONSchema()
		} else {
			out[tool.Name] = map[string]any{}
		}
	}
	return out
}

func testfileDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
