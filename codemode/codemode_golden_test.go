package codemode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestSDKAndSchemaGoldens(t *testing.T) {
	builder := tooltest.CalcBuilder(t)

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
				Context: map[string]any{"fixed_a": 42},
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
				Context: map[string]any{"fixed_a": 42},
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
			resolved, err := builder.Resolve(v.cfg)
			if err != nil {
				t.Fatalf("resolve toolset: %v", err)
			}

			prefix := "calc-" + v.name
			checkGolden(t, goldenPath(prefix+".sdk.ts"), typecheckSDKSource(resolved))
			checkGolden(t, goldenPath(prefix+".d.ts"), DeclarationSource(resolved))
			checkGolden(t, goldenPath(prefix+".schema.json"), schemaGoldenSource(t, resolved))
		})
	}
}

func TestEdgeCaseGoldens(t *testing.T) {
	builder := tooltest.EdgeCasesBuilder(t)

	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}

	checkGolden(t, goldenPath("edge-cases-passthrough.d.ts"), DeclarationSource(resolved))
	checkGolden(t, goldenPath("edge-cases-passthrough.schema.json"), schemaGoldenSource(t, resolved))
}

func TestSharedTypesGoldens(t *testing.T) {
	builder := tooltest.SharedTypesBuilder(t)

	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}

	checkGolden(t, goldenPath("shared-types-passthrough.d.ts"), DeclarationSource(resolved))
	checkGolden(t, goldenPath("shared-types-passthrough.schema.json"), schemaGoldenSource(t, resolved))
}

func TestGithubIssuesGoldens(t *testing.T) {
	builder := tooltest.GithubIssuesBuilder(t)

	resolved, err := builder.Resolve(toolset.Config{})
	if err != nil {
		t.Fatalf("resolve toolset: %v", err)
	}

	checkGolden(t, goldenPath("github-issues-passthrough.d.ts"), DeclarationSource(resolved))
	checkGolden(t, goldenPath("github-issues-passthrough.schema.json"), schemaGoldenSource(t, resolved))
}

func goldenPath(name string) string {
	return filepath.Join(testfileDir(), "..", "testutil", "testdata", "goldens", "codemode", name)
}

func schemaGoldenSource(t *testing.T, resolved toolset.ResolvedToolset) string {
	t.Helper()
	view := resolved.AgentView()
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

// buildSchemaMap builds a map of tool name to JSON Schema for all tools in the view.
func buildSchemaMap(view toolset.AgentView) map[string]any {
	tools := sortedTools(view)

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

// testfileDir returns the directory containing this test file.
func testfileDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
