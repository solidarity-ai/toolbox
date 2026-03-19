package invoke_test

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/solidarity-ai/toolbox/invoke"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

const workspaceToolsPackageDir = "github.com/solidarity-ai/google-workspace-tools"

func TestRunRoutesTSToolExecThroughTSWasmer(t *testing.T) {
	resolved := toolset.NewResolvedToolset([]toolset.Tool{
		{
			Name:        "users.list",
			Description: "List Google Workspace users",
			TS:          mustWorkspaceUsersListToolDef(t),
		},
	})

	got, err := invoke.Run(resolved, "users.list", map[string]any{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var decoded struct {
		Runtime string   `json:"runtime"`
		Binary  string   `json:"binary"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if decoded.Runtime != "tswasmer-stub" {
		t.Fatalf("expected runtime tswasmer-stub, got %q", decoded.Runtime)
	}
	if decoded.Binary != "gwc" {
		t.Fatalf("expected binary gwc, got %q", decoded.Binary)
	}

	wantArgs := []string{"users", "list", "--format", "json"}
	if len(decoded.Args) != len(wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, decoded.Args)
	}
	for i := range wantArgs {
		if decoded.Args[i] != wantArgs[i] {
			t.Fatalf("expected args %v, got %v", wantArgs, decoded.Args)
		}
	}
}

func mustWorkspaceUsersListToolDef(t testing.TB) *tooldef.TSToolDef {
	t.Helper()

	return &tooldef.TSToolDef{
		Entry: workspaceToolsPackageDir + "/tools/users.list.ts",
		Files: fstest.MapFS{
			workspaceToolsPackageDir + "/manifest.json": {
				Data: []byte(`{
  "package": {
    "path": "github.com/solidarity-ai/google-workspace-tools",
    "version": "v0.0.0",
    "description": "Stub Google Workspace tools"
  },
  "assets": {
    "gwc": {
      "type": "wasm"
    }
  }
}`),
			},
			workspaceToolsPackageDir + "/tools/users.list.ts": {
				Data: []byte(`declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

export const params = {};
export const metadata = {
  readOnly: true,
  idempotent: true,
  description: "List Google Workspace users"
};

export async function execute(params: Record<string, never>, ctx: unknown) {
  const result = await exec("gwc", ["users", "list", "--format", "json"]);
  return result.stdout;
}
`),
			},
		},
	}
}
