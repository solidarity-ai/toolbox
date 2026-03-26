package codemode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

	testdataDir := filepath.Join(testfileDir(), "..", "testutil", "testdata", "goldens", "codemode")
	generateGoldens := os.Getenv("GENERATE_GOLDENS") == "1"

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			resolved, err := builder.Resolve(v.cfg)
			if err != nil {
				t.Fatalf("resolve toolset: %v", err)
			}

			// --- SDK source golden ---
			sdkSource := typecheckSDKSource(resolved)
			sdkFile := filepath.Join(testdataDir, "calc-"+v.name+".sdk.ts")

			if generateGoldens {
				if err := os.MkdirAll(testdataDir, 0o755); err != nil {
					t.Fatalf("create testdata dir: %v", err)
				}
				if err := os.WriteFile(sdkFile, []byte(sdkSource), 0o644); err != nil {
					t.Fatalf("write sdk golden: %v", err)
				}
				t.Logf("wrote %s", sdkFile)
			} else {
				want, err := os.ReadFile(sdkFile)
				if err != nil {
					t.Fatalf("read sdk golden (run with GENERATE_GOLDENS=1 to create): %v", err)
				}
				if diff := cmp.Diff(string(want), sdkSource); diff != "" {
					t.Errorf("sdk golden mismatch (-want +got):\n%s", diff)
				}
			}

			// --- MCP schema golden ---
			view := resolved.AgentView()
			schemaMap := buildSchemaMap(view)
			schemaJSON, err := json.MarshalIndent(schemaMap, "", "  ")
			if err != nil {
				t.Fatalf("marshal schema: %v", err)
			}
			schemaJSON = append(schemaJSON, '\n')

			schemaFile := filepath.Join(testdataDir, "calc-"+v.name+".schema.json")

			if generateGoldens {
				if err := os.WriteFile(schemaFile, schemaJSON, 0o644); err != nil {
					t.Fatalf("write schema golden: %v", err)
				}
				t.Logf("wrote %s", schemaFile)
			} else {
				want, err := os.ReadFile(schemaFile)
				if err != nil {
					t.Fatalf("read schema golden (run with GENERATE_GOLDENS=1 to create): %v", err)
				}
				if diff := cmp.Diff(string(want), string(schemaJSON)); diff != "" {
					t.Errorf("schema golden mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// buildSchemaMap builds a map of tool name to JSON Schema for all tools in the view.
func buildSchemaMap(view toolset.AgentView) map[string]any {
	tools := make([]toolset.AgentTool, len(view.Tools))
	copy(tools, view.Tools)
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

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
